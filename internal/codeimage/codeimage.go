// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package codeimage extracts Go code from images.
package codeimage

import (
	"context"
	"errors"
	"fmt"
	"go/format"
	"io"
	"net/http"
	"strconv"

	"golang.org/x/oscar/internal/llm"
)

// InURL looks for code in the image referred to by url.
// It fetches the url using the given HTTP client, then calls [InBlob].
func InURL(ctx context.Context, url string, client *http.Client, cgen llm.ContentGenerator) (string, error) {
	res, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s returned status %s", url, res.Status)
	}
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	return InBlob(ctx, llm.Blob{
		MIMEType: res.Header.Get("Content-Type"),
		Data:     data,
	}, cgen)
}

// InBlob looks for code in the given blob.
// On success, it returns a properly formatted Go program or program fragment.
func InBlob(ctx context.Context, blob llm.Blob, cgen llm.ContentGenerator) (string, error) {
	parts := []llm.Part{
		llm.Text(instructions),
		blob,
	}
	schema := &llm.Schema{
		Type:        llm.TypeString,
		Description: "the program",
	}
	// Retry several times in the hope that the LLM will eventually produce a valid program.
	for try := 0; try < 3; try++ {
		output, err := cgen.GenerateContent(ctx, schema, parts)
		if err != nil {
			return "", err
		}
		unq, err := strconv.Unquote(output)
		if err != nil {
			return "", fmt.Errorf("strconv.Unquote: %w", err)
		}
		fbytes, err := format.Source([]byte(unq))
		if err != nil {
			// Retry if it isn't a valid program.
			continue
		}
		return string(fbytes), nil
	}
	return "", errors.New("could not produce a valid Go program")
}

const instructions = `
The following image contains code in the Go programming language.
Extract the Go code from the image.
Make sure you produce a syntactically valid Go program.
`

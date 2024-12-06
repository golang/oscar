// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package codeimage

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/oscar/internal/llm"
)

var ctx = context.Background()

func TestInBlob(t *testing.T) {
	imageFiles, err := filepath.Glob(filepath.Join("testdata", "*.png"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ifile := range imageFiles {
		noext := strings.TrimSuffix(ifile, ".png")
		t.Run(filepath.Base(noext), func(t *testing.T) {
			wbytes, err := os.ReadFile(noext + ".go")
			if err != nil {
				t.Fatal(err)
			}
			gen := newMockGenerator(string(wbytes))
			data, err := os.ReadFile(ifile)
			if err != nil {
				t.Fatal(err)
			}
			blob := llm.Blob{MIMEType: "image/png", Data: data}
			got, err := InBlob(ctx, blob, gen)
			if err != nil {
				t.Fatal(err)
			}
			got = removeBlankLines(got)
			want := removeBlankLines(string(wbytes))
			if got != want {
				t.Errorf("\ngot:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func removeBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	lines = slices.DeleteFunc(lines, func(s string) bool {
		return len(strings.TrimSpace(s)) == 0
	})
	return strings.Join(lines, "\n")
}

func newMockGenerator(result string) llm.ContentGenerator {
	return llm.TestContentGenerator("imageMock",
		func(context.Context, *llm.Schema, []llm.Part) (string, error) {
			return strconv.Quote(result), nil
		})
}

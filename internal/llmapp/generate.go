// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llmapp

import (
	"context"
	"crypto/sha256"
	"encoding/json"

	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
	"rsc.io/ordered"
)

// generateText returns a (possibly cached) text response for the prompts.
func (c *Client) generateText(ctx context.Context, prompts []any) (_ string, cached bool, _ error) {
	model := c.g.Model()
	h := hash(prompts)
	k := ordered.Encode(generateTextKind, model, h)
	c.db.Lock(string(k))
	defer c.db.Unlock(string(k))

	r := c.load(k)
	if r != nil {
		// cache hit
		return r.Response, true, nil
	}

	// cache miss
	result, err := c.g.GenerateText(ctx, prompts...)
	if err != nil {
		return "", false, err
	}
	c.db.Set(k, storage.JSON(response{
		Model:      model,
		PromptHash: h,
		Response:   result,
	}))
	return result, false, nil
}

// Cache key context.
const generateTextKind = "llmapp.GenerateText"

// load loads a cached response from the database.
// load returns nil if the response cannot be unmarshaled
// or there is no entry for the key.
func (c *Client) load(key []byte) *response {
	if cached, ok := c.db.Get(key); ok {
		var r response
		// Unmarshal will only fail if a backwards-incompatible change
		// is made to the [response] struct.
		if err := json.Unmarshal(cached, &r); err != nil {
			c.slog.Error("cannot unmarshal cached response", "err", err)
			return nil
		}
		return &r
	}
	return nil
}

// response is a cached response to a
// [llm.TextGenerator.GenerateText] query.
type response struct {
	// The generative model used to generate the response.
	Model string
	// The SHA-256 hash of the prompts used to generate the response.
	PromptHash []byte
	// The generated response.
	Response string
}

// hash returns the SHA-256 hash of the strings or blobs.
func hash(parts []any) []byte {
	h := sha256.New()
	for _, p := range parts {
		switch p := p.(type) {
		case string:
			h.Write([]byte(p))
		case llm.Blob:
			h.Write([]byte(p.MIMEType))
			h.Write(p.Data)
		}
	}
	return h.Sum(nil)
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llmapp

import (
	"context"

	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
)

// generate returns a (possibly cached) response for the prompts.
func (c *Client) generate(ctx context.Context, schema *llm.Schema, prompts []llm.Part) (string, bool, error) {
	k, h := c.keyAndHashGenerateContent(schema, prompts)
	c.db.Lock(string(k))
	defer c.db.Unlock(string(k))

	r := load[responseGenerateContent](c, k)
	if r != nil {
		// cache hit
		return r.Response, true, nil
	}

	// cache miss
	result, err := c.g.GenerateContent(ctx, schema, prompts)
	if err != nil {
		return "", false, err
	}

	c.db.Set(k, storage.JSON(responseGenerateContent{
		Model:      c.g.Model(),
		PromptHash: h,
		Response:   result,
	}))
	return result, false, nil
}

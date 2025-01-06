// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llmapp

import (
	"crypto/sha256"
	"encoding/json"
	"hash"

	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
	"rsc.io/ordered"
)

// Cache key contexts.
//
// The llmapp cache stores the following database entries:
//
//   - ("llmapp.GenerateText", model, SHA-256(schema, prompts)) -> [responseGenerateContent]
//     where model is the name of the generative model used to generate responses, schema is
//     the input schema to the model, and prompts are the input prompts.
//
//   - ("llmapp.CheckPolicy", checker, SHA-256(policies, input, prompts)) -> [responseCheckText]
//     where checker is the name of the policy checker used to check LLM inputs/outputs,
//     policies are the applied policies, input is the text to check, and prompts are the
//     optional prompts used to generate the input (only relevant if the input is itself
//     an LLM output).
const (
	generateKind = "llmapp.GenerateText"
	checkKind    = "llmapp.CheckPolicy"
)

// load loads a cached response from the database.
// load returns nil if the response cannot be unmarshaled
// or there is no entry for the key.
func load[R any](c *Client, key []byte) *R {
	cached, ok := c.db.Get(key)
	if !ok {
		return nil
	}
	var r R
	if err := json.Unmarshal(cached, &r); err != nil {
		c.slog.Error("llmapp.load: cannot unmarshal cached response", "err", err, "key", key, "cached", string(cached))
		return nil
	}
	return &r
}

// responseGenerateContent is a cached response to an [llm.ContentGenerator.GenerateContent] query.
type responseGenerateContent struct {
	// The generative model used to generate the response.
	Model string
	// The SHA-256 hash of the schema and prompts used to generate the response.
	PromptHash []byte
	// The raw generated response.
	Response string
}

// keyAndHashGenerateContent returns the database key and input hash (hash of schema and parts)
// for cached responses from [llm.ContentGenerator.GenerateContent] queries.
func (c *Client) keyAndHashGenerateContent(schema *llm.Schema, parts []llm.Part) (key, hash []byte) {
	h := sha256.New()
	writeObjectToHash(h, schema)
	c.writePromptsToHash(h, parts)
	hash = h.Sum(nil)
	key = ordered.Encode(generateKind, c.g.Model(), hash)
	return key, hash
}

// responseCheckText is a cached result of a [llm.PolicyChecker.CheckText] call.
type responseCheckText struct {
	// The name of the PolicyChecker used to generate this response.
	Name string
	// The SHA-256 hash of the inputs to CheckText (policies, input text and prompts).
	InputHash []byte
	// The result returned by CheckText.
	Response []*llm.PolicyResult
}

// keyAndHashCheckText returns the DB key for the cache entry,
// and the SHA-256 hash of the inputs to [llm.PolicyChecker.CheckText]
// (policies, input text and optional prompts).
func (c *Client) keyAndHashCheckText(policies []*llm.PolicyConfig, text string, prompts []llm.Part) (key, hash []byte) {
	h := sha256.New()
	writeObjectToHash(h, policies)
	if text != "" {
		h.Write([]byte(text))
	}
	c.writePromptsToHash(h, prompts)
	hash = h.Sum(nil)
	key = ordered.Encode(checkKind, c.checker.Name(), hash)
	return key, hash
}

// writeObjectToHash writes the JSON representation of the object
// to the hash if the object is non-nil.
func writeObjectToHash(h hash.Hash, obj any) {
	if obj != nil {
		h.Write(storage.JSON(obj))
	}
}

// writePromptsToHash writes the given prompts (text or blob) to the hash.
func (c *Client) writePromptsToHash(h hash.Hash, prompts []llm.Part) {
	for _, p := range prompts {
		switch p := p.(type) {
		case llm.Text:
			h.Write([]byte(p))
		case llm.Blob:
			h.Write([]byte(p.MIMEType))
			h.Write(p.Data)
		default:
			c.db.Panic("llmapp.Client.writePromptsToHash: unknown prompt type", "prompt", p)
		}
	}
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llmapp

import "golang.org/x/oscar/internal/llm"

// A Doc is a document to provide to an LLM as part of a prompt.
type Doc struct {
	// Freeform text describing the type of document.
	// Used to help the LLM distinguish between kinds of
	// documents, or understand their relative importance.
	Type string `json:"type,omitempty"`
	// The URL of the document, if one exists.
	URL string `json:"url,omitempty"`
	// The author of the document, if known.
	Author string `json:"author,omitempty"`
	// The title of the document, if known.
	Title string `json:"title,omitempty"`
	Text  string `json:"text"` // required
}

// Result is the result of an LLM call.
type Result struct {
	Response string      // the raw LLM-generated response
	Cached   bool        // whether the response was cached
	Schema   *llm.Schema // the JSON schema used to generate the result (nil if none)
	Prompt   []any       // the prompt(s) used to generate the result
}

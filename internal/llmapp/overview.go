// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package llmapp provides applications for LLM content generation
// to complete higher-level tasks.
package llmapp

import (
	"context"
	"errors"

	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
)

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

// Overview returns an LLM-generated overview of the given documents,
// styled with markdown.
// It returns an error if no documents are provided, or the LLM
// is unable to generate a response.
func Overview(ctx context.Context, g llm.TextGenerator, docs ...*Doc) (string, error) {
	if len(docs) == 0 {
		return "", errors.New("llmapp.Overview: no documents")
	}
	return g.GenerateText(ctx, OverviewPrompt(docs)...)
}

// OverviewPrompt converts the given docs into a slice of
// text prompts, followed by a hard-coded instruction prompt.
func OverviewPrompt(docs []*Doc) []string {
	var inputs = make([]string, len(docs))
	for i, d := range docs {
		inputs[i] = string(storage.JSON(d))
	}
	return append(inputs, instruction)
}

// The hard-coded instruction to use when generating an overview.
// In the future this might be a client-provided input to [Overview]
// instead.
const instruction = `Write a detailed summary of the documents.
Use markdown formatting with headings and lists.`

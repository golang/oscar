// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package llmapp provides applications for LLM content generation
// to complete higher-level tasks.
package llmapp

import (
	"context"
	"embed"
	_ "embed"
	"errors"
	"strings"
	"text/template"

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
// The kind argument is a descriptor for the given documents, used to add
// additional context to the LLM prompt.
// Overview returns an error if no documents are provided or the LLM is unable
// to generate a response.
func Overview(ctx context.Context, g llm.TextGenerator, kind DocsKind, docs ...*Doc) (string, error) {
	if len(docs) == 0 {
		return "", errors.New("llmapp.Overview: no documents")
	}
	return g.GenerateText(ctx, OverviewPrompt(kind, docs)...)
}

// OverviewPrompt converts the given docs into a slice of
// text prompts, followed by an instruction prompt based
// on the documents kind.
func OverviewPrompt(kind DocsKind, docs []*Doc) []string {
	var inputs = make([]string, len(docs))
	for i, d := range docs {
		inputs[i] = string(storage.JSON(d))
	}
	return append(inputs, kind.instructions())
}

// DocsKind is a descriptor for a group of documents.
type DocsKind struct{ int }

var (
	// Represents a group of documents of an unspecified kind.
	Documents DocsKind = DocsKind{}
	// The documents represent a post and comments/replies
	// on that post. For example, a GitHub issue and its comments.
	PostAndComments DocsKind = DocsKind{1}
)

// IsPostAndComments reports whether k is of kind [PostAndComments].
func (k DocsKind) IsPostAndComments() bool {
	return k == PostAndComments
}

//go:embed prompts/*.tmpl
var promptFS embed.FS
var instructionTmpl = template.Must(template.ParseFS(promptFS, "prompts/overview.tmpl"))

// instructions returns the instruction prompt for the given
// document kind.
func (k DocsKind) instructions() string {
	w := &strings.Builder{}
	err := instructionTmpl.Execute(w, k)
	if err != nil {
		// unreachable except bug in this package
		panic(err)
	}
	return w.String()
}

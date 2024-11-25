// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package llmapp provides applications for LLM content generation
// to complete higher-level tasks.
//
// All functionality is provided by [Client], created by [New].
//
// Cached LLM responses are stored in the Client's database as:
//
//	("llmapp.GenerateText", generativeModel, promptHash) -> [response]
//
// Note that currently there is no clear way to clean up old cache values
// that are no longer relevant, but we might want to add this in the future.
//
// We can, however, easily delete ALL cache values and start over by deleting
// all database entries starting with "llmapp.GenerateText".
package llmapp

import (
	"context"
	"embed"
	_ "embed"
	"errors"
	"log/slog"
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

// OverviewResult is the result of [Overview] or [PostOverview].
type OverviewResult struct {
	Overview string // the LLM-generated summary
	Cached   bool   // whether the summary was cached
	Prompt   []any  // the prompt(s) used to generate the result
}

// Client is a client for accessing the LLM application functionality.
type Client struct {
	slog *slog.Logger
	g    llm.ContentGenerator
	db   storage.DB // cache for LLM responses
}

// New returns a new client.
// g is the underlying LLM content generator to use, and db is the database
// to use as a cache.
func New(lg *slog.Logger, g llm.ContentGenerator, db storage.DB) *Client {
	return &Client{slog: lg, g: g, db: db}
}

// Overview returns an LLM-generated overview of the given documents,
// styled with markdown.
// Overview returns an error if no documents are provided or the LLM is unable
// to generate a response.
func (c *Client) Overview(ctx context.Context, docs ...*Doc) (*OverviewResult, error) {
	return c.overview(ctx, documents, &docGroup{docs: docs})
}

// PostOverview returns an LLM-generated overview of the given post and comments,
// styled with markdown.
// PostOverview returns an error if no post is provided or the LLM is unable to generate a response.
func (c *Client) PostOverview(ctx context.Context, post *Doc, comments []*Doc) (*OverviewResult, error) {
	if post == nil {
		return nil, errors.New("llmapp PostOverview: no post")
	}
	return c.overview(ctx, postAndComments,
		&docGroup{label: "post", docs: []*Doc{post}},
		&docGroup{label: "comments", docs: comments})
}

// RelatedOverview returns an LLM-generated overview of the given document and
// related documents, styled with markdown.
// RelatedOverview returns an error if no initial document is provided, no related docs are
// provided, or the LLM is unable to generate a response.
func (c *Client) RelatedOverview(ctx context.Context, doc *Doc, related []*Doc) (*OverviewResult, error) {
	if doc == nil {
		return nil, errors.New("llmapp RelatedOverview: no doc")
	}
	if len(related) == 0 {
		return nil, errors.New("llmapp RelatedOverview: no related docs")
	}
	return c.overview(ctx, docAndRelated,
		&docGroup{label: "original", docs: []*Doc{doc}},
		&docGroup{label: "related", docs: related},
	)
}

// UpdatedPostOverview returns an LLM-generated overview of the given post and comments,
// styled with markdown. It summarizes the oldComments and newComments separately.
// UpdatedPostOverview returns an error if no post is provided or the LLM is unable to generate a response.
func (c *Client) UpdatedPostOverview(ctx context.Context, post *Doc, oldComments, newComments []*Doc) (*OverviewResult, error) {
	if post == nil {
		return nil, errors.New("llmapp PostOverview: no post")
	}
	return c.overview(ctx, postAndCommentsUpdated,
		&docGroup{label: "post", docs: []*Doc{post}},
		&docGroup{label: "old comments", docs: oldComments},
		&docGroup{label: "new comments", docs: newComments},
	)
}

// a docGroup is a group of documents.
type docGroup struct {
	label string // (optional) label for the group to give to the LLM.
	docs  []*Doc
}

// overview returns an LLM-generated overview of the given documents,
// styled with markdown.
// The kind argument is a descriptor for the given documents, used to add
// additional context to the LLM prompt.
// Overview returns an error if no documents are provided or the LLM is unable
// to generate a response.
func (c *Client) overview(ctx context.Context, kind docsKind, groups ...*docGroup) (*OverviewResult, error) {
	if len(groups) == 0 {
		return nil, errors.New("llmapp overview: no documents")
	}
	prompt := overviewPrompt(kind, groups)
	overview, cached, err := c.generateText(ctx, prompt)
	if err != nil {
		return nil, err
	}
	return &OverviewResult{
		Overview: overview,
		Cached:   cached,
		Prompt:   prompt,
	}, nil
}

// overviewPrompt converts the given docs into a slice of
// text prompts, followed by an instruction prompt based
// on the documents kind.
func overviewPrompt(kind docsKind, groups []*docGroup) []any {
	var inputs []any
	for _, g := range groups {
		if g.label != "" {
			inputs = append(inputs, g.label)
		}
		for _, d := range g.docs {
			inputs = append(inputs, string(storage.JSON(d)))
		}
	}
	return append(inputs, kind.instructions())
}

// docsKind is a descriptor for a group of documents.
type docsKind string

var (
	// Represents a group of documents of an unspecified kind.
	documents docsKind = "documents"
	// The documents represent a post and comments/replies
	// on that post. For example, a GitHub issue and its comments.
	postAndComments docsKind = "post_and_comments"
	// The documents represent a post and comments,
	// followed by a list of *new* comments on that post.
	postAndCommentsUpdated docsKind = "post_and_comments_updated"
	// The documents represent a document followed by documents
	// that are related to it in some way.
	docAndRelated docsKind = "doc_and_related"
)

//go:embed prompts/*.tmpl
var promptFS embed.FS
var tmpls = template.Must(template.ParseFS(promptFS, "prompts/*.tmpl"))

// instructions returns the instruction prompt for the given
// document kind.
func (k docsKind) instructions() string {
	w := &strings.Builder{}
	err := tmpls.ExecuteTemplate(w, string(k), nil)
	if err != nil {
		// unreachable except bug in this package
		panic(err)
	}
	return w.String()
}

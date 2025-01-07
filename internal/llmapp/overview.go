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

// Client is a client for accessing the LLM application functionality.
type Client struct {
	slog    *slog.Logger
	g       llm.ContentGenerator
	checker llm.PolicyChecker
	db      storage.DB // cache for LLM responses
}

// New returns a new client.
// g is the underlying LLM content generator to use, and db is the database
// to use as a cache.
func New(lg *slog.Logger, g llm.ContentGenerator, db storage.DB) *Client {
	return NewWithChecker(lg, g, nil, db)
}

// Overview returns an LLM-generated overview of the given documents,
// styled with markdown.
// Overview returns an error if no documents are provided or the LLM is unable
// to generate a response.
func (c *Client) Overview(ctx context.Context, docs ...*Doc) (*Result, error) {
	return c.overview(ctx, documents, &docGroup{docs: docs})
}

// PostOverview returns an LLM-generated overview of the given post and comments,
// styled with markdown.
// PostOverview returns an error if no post is provided or the LLM is unable to generate a response.
func (c *Client) PostOverview(ctx context.Context, post *Doc, comments []*Doc) (*Result, error) {
	if post == nil {
		return nil, errors.New("llmapp PostOverview: no post")
	}
	return c.overview(ctx, postAndComments,
		&docGroup{label: "post", docs: []*Doc{post}},
		&docGroup{label: "comments", docs: comments})
}

// UpdatedPostOverview returns an LLM-generated overview of the given post and comments,
// styled with markdown. It summarizes the oldComments and newComments separately.
// UpdatedPostOverview returns an error if no post is provided or the LLM is unable to generate a response.
func (c *Client) UpdatedPostOverview(ctx context.Context, post *Doc, oldComments, newComments []*Doc) (*Result, error) {
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

// overview returns an LLM-generated overview of the given documents.
// The kind argument is a descriptor for the given documents, used to
// determine which prompt and schema to pass to to the LLM.
// overview returns an error if no documents are provided or the LLM is unable
// to generate a response.
func (c *Client) overview(ctx context.Context, kind docsKind, groups ...*docGroup) (*Result, error) {
	if len(groups) == 0 {
		return nil, errors.New("llmapp overview: no documents")
	}
	prompt := prompt(kind, groups)
	schema := kind.schema()
	overview, cached, err := c.generate(ctx, schema, prompt)
	if err != nil {
		return nil, err
	}
	return &Result{
		Response:         overview,
		Cached:           cached,
		Schema:           schema,
		Prompt:           prompt,
		PolicyEvaluation: c.evaluatePolicy(ctx, prompt, overview),
	}, nil
}

// prompt converts the given docs into a slice of
// text prompts, followed by an instruction prompt based
// on the documents kind.
func prompt(kind docsKind, groups []*docGroup) []llm.Part {
	var inputs []llm.Part
	for _, g := range groups {
		if g.label != "" {
			inputs = append(inputs, llm.Text(g.label))
		}
		for _, d := range g.docs {
			inputs = append(inputs, llm.Text(storage.JSON(d)))
		}
	}
	return append(inputs, llm.Text(kind.instructions()))
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

// schema returns the JSON schema for the given document kind,
// or nil if there is no corresponding JSON schema.
// TODO(tatianabradley): Use schemas instead of unstructured
// prompts for all [docsKind]s.
func (k docsKind) schema() *llm.Schema {
	if k == docAndRelated {
		return relatedSchema
	}
	return nil
}

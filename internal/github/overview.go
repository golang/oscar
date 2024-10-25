// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"context"

	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/llmapp"
	"golang.org/x/oscar/internal/storage"
)

// IssueOverviewResult is the result of [IssueOverview].
// It contains the generated overview and metadata about
// the issue.
type IssueOverviewResult struct {
	URL         string   // the issue's URL
	NumComments int      // number of comments for this issue
	Overview    string   // the LLM-generated issue and comment summary
	Prompt      []string // the prompt(s) used to generate the result
}

// IssueOverview returns an LLM-generated overview of the issue and its comments.
// It does not make any requests to GitHub; the issue and comment data must already
// be stored in db.
func IssueOverview(ctx context.Context, g llm.TextGenerator, db storage.DB, project string, issue int64) (*IssueOverviewResult, error) {
	var docs []*llmapp.Doc
	var m issueMetadata
	for e := range events(db, project, issue, issue) {
		m.update(e)
		doc := e.toLLMDoc()
		if doc == nil {
			continue
		}
		docs = append(docs, doc)
	}
	overview, err := llmapp.Overview(ctx, g, llmapp.PostAndComments, docs...)
	if err != nil {
		return nil, err
	}
	return &IssueOverviewResult{
		URL:         m.url,
		NumComments: m.numComments,
		Overview:    overview,
		Prompt:      llmapp.OverviewPrompt(llmapp.PostAndComments, docs),
	}, nil
}

type issueMetadata struct {
	url         string
	numComments int
}

// update updates the issueMetadata given the event.
func (m *issueMetadata) update(e *Event) {
	switch v := e.Typed.(type) {
	case *Issue:
		m.url = v.HTMLURL
	case *IssueComment:
		m.numComments++
	}
}

// toLLMDoc converts an Event to a format that can be used as
// an input to an LLM.
// It returns nil if the Event cannot be converted to a document.
func (e *Event) toLLMDoc() *llmapp.Doc {
	switch v := e.Typed.(type) {
	case *Issue:
		return &llmapp.Doc{
			Type:   "issue",
			URL:    v.HTMLURL,
			Author: v.User.Login,
			Title:  v.Title,
			Text:   v.Body,
		}
	case *IssueComment:
		return &llmapp.Doc{
			Type:   "issue comment",
			URL:    v.HTMLURL,
			Author: v.User.Login,
			// no title
			Text: v.Body,
		}
	}
	return nil
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"context"

	"golang.org/x/oscar/internal/llmapp"
	"golang.org/x/oscar/internal/storage"
)

// IssueOverviewResult is the result of [IssueOverview].
// It contains the generated overview and metadata about
// the issue.
type IssueOverviewResult struct {
	*Issue          // the issue itself
	NumComments int // number of comments for this issue

	Overview *llmapp.OverviewResult // the LLM-generated issue and comment summary
}

// IssueOverview returns an LLM-generated overview of the issue and its comments.
// It does not make any requests to GitHub; the issue and comment data must already
// be stored in db.
func IssueOverview(ctx context.Context, lc *llmapp.Client, db storage.DB, project string, issue int64) (*IssueOverviewResult, error) {
	var iss *Issue
	var post *llmapp.Doc
	var comments []*llmapp.Doc
	for e := range events(db, project, issue, issue) {
		doc, isIssue := e.toLLMDoc()
		if doc == nil {
			continue
		}
		if isIssue {
			iss = e.Typed.(*Issue)
			post = doc
			continue
		}
		comments = append(comments, doc)
	}
	overview, err := lc.PostOverview(ctx, post, comments)
	if err != nil {
		return nil, err
	}
	return &IssueOverviewResult{
		Issue:       iss,
		NumComments: len(comments),
		Overview:    overview,
	}, nil
}

// toLLMDoc converts an Event to a format that can be used as
// an input to an LLM.
// isIssue is true if the event represents a GitHub issue (as opposed to
// a comment or another event).
// It returns (nil, false) if the Event cannot be converted to a document.
func (e *Event) toLLMDoc() (_ *llmapp.Doc, isIssue bool) {
	switch v := e.Typed.(type) {
	case *Issue:
		return &llmapp.Doc{
			Type:   "issue",
			URL:    v.HTMLURL,
			Author: v.User.Login,
			Title:  v.Title,
			Text:   v.Body,
		}, true
	case *IssueComment:
		return &llmapp.Doc{
			Type:   "issue comment",
			URL:    v.HTMLURL,
			Author: v.User.Login,
			// no title
			Text: v.Body,
		}, false
	}
	return nil, false
}

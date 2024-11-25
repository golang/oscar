// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"context"
	"fmt"
	"iter"

	"golang.org/x/oscar/internal/llmapp"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

// IssueOverviewResult is the result of [IssueOverview].
// It contains the generated overview and metadata about the issue.
type IssueOverviewResult struct {
	TotalComments int            // number of comments for this issue
	Overview      *llmapp.Result // the LLM-generated issue and comment summary
}

// IssueOverview returns an LLM-generated overview of the issue and its comments.
// It does not make any requests to GitHub; the issue and comment data must already
// be stored in db.
func IssueOverview(ctx context.Context, lc *llmapp.Client, db storage.DB, iss *Issue) (*IssueOverviewResult, error) {
	post := iss.toLLMDoc()
	var cs []*llmapp.Doc
	for c := range comments(db, iss.Project(), iss.Number) {
		cs = append(cs, c.toLLMDoc())
	}
	overview, err := lc.PostOverview(ctx, post, cs)
	if err != nil {
		return nil, err
	}
	return &IssueOverviewResult{
		TotalComments: len(cs),
		Overview:      overview,
	}, nil
}

// UpdateOverviewResult is the result of [UpdateOverview].
// It contains the generated overview and metadata about the issue.
type UpdateOverviewResult struct {
	NewComments   int            // number of new comments for this issue
	TotalComments int            // number of comments for this issue
	Overview      *llmapp.Result // the LLM-generated issue and comment summary
}

// UpdateOverview returns an LLM-generated overview of the issue and its
// comments, separating the comments into "old" and "new" groups broken
// by the specifed lastRead comment id. (The lastRead comment itself is
// considered "old", and must be present in the database).
// UpdateOverview does not make any requests to GitHub; the issue and comment data must already
// be stored in db.
func UpdateOverview(ctx context.Context, lc *llmapp.Client, db storage.DB,
	iss *Issue, lastRead int64) (*UpdateOverviewResult, error) {
	post := iss.toLLMDoc()
	var oldComments, newComments []*llmapp.Doc
	foundTarget := false
	for c := range comments(db, iss.Project(), iss.Number) {
		// New comment.
		if c.CommentID() > lastRead {
			newComments = append(newComments, c.toLLMDoc())
			continue
		}
		if c.CommentID() == lastRead {
			foundTarget = true
		}
		oldComments = append(oldComments, c.toLLMDoc())
	}
	if !foundTarget {
		return nil, fmt.Errorf("issue %d comment %d not found in database", iss.Number, lastRead)
	}
	overview, err := lc.UpdatedPostOverview(ctx, post, oldComments, newComments)
	if err != nil {
		return nil, err
	}
	return &UpdateOverviewResult{
		NewComments:   len(newComments),
		TotalComments: len(oldComments) + len(newComments),
		Overview:      overview,
	}, nil
}

// toLLMDoc converts an Issue to a format that can be used as
// an input to an LLM.
func (i *Issue) toLLMDoc() *llmapp.Doc {
	return &llmapp.Doc{
		Type:   "issue",
		URL:    i.HTMLURL,
		Author: i.User.Login,
		Title:  i.Title,
		Text:   i.Body,
	}
}

// toLLMDoc converts an IssueComment to a format that can be used as
// an input to an LLM.
func (ic *IssueComment) toLLMDoc() *llmapp.Doc {
	return &llmapp.Doc{
		Type:   "issue comment",
		URL:    ic.HTMLURL,
		Author: ic.User.Login,
		// no title
		Text: ic.Body,
	}
}

// comments returns an iterator over the comments for the issue in the db.
func comments(db storage.DB, project string, issue int64) iter.Seq[*IssueComment] {
	return func(yield func(*IssueComment) bool) {
		for e := range eventsByAPI(db, project, issue, "/issues/comments") {
			if !yield(e.Typed.(*IssueComment)) {
				return
			}
		}
	}
}

// eventsByAPI returns an iterator over the events for the issue in the db
// with the given API.
func eventsByAPI(db storage.DB, project string, issue int64, api string) iter.Seq[*Event] {
	return func(yield func(*Event) bool) {
		start := o(project, issue, api)
		end := o(project, issue, api, ordered.Inf)
		for t := range timed.Scan(db, eventKind, start, end) {
			if !yield(decodeEvent(db, t)) {
				return
			}
		}
	}
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package overview

import (
	"context"
	"fmt"

	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/llmapp"
)

// IssueResult is the result of [Client.ForIssue].
// It contains the generated overview and metadata about the issue.
type IssueResult struct {
	TotalComments int            // number of comments for this issue
	Overview      *llmapp.Result // the LLM-generated issue and comment summary
}

// ForIssue returns an LLM-generated overview of the issue and its comments.
// It does not make any requests to or modify, GitHub; the issue and comment data must already
// be stored in the database.
func (c *Client) ForIssue(ctx context.Context, iss *github.Issue) (*IssueResult, error) {
	post := iss.ToLLMDoc()
	var cs []*llmapp.Doc
	for c := range c.gh.Comments(iss) {
		cs = append(cs, c.ToLLMDoc())
	}
	overview, err := c.lc.PostOverview(ctx, post, cs)
	if err != nil {
		return nil, err
	}
	return &IssueResult{
		TotalComments: len(cs),
		Overview:      overview,
	}, nil
}

// IssueUpdateResult is the result of [Client.ForIssueUpdate].
// It contains the generated overview and metadata about the issue.
type IssueUpdateResult struct {
	NewComments   int            // number of new comments for this issue
	TotalComments int            // number of comments for this issue
	Overview      *llmapp.Result // the LLM-generated issue and comment summary
}

// ForIssueUpdate returns an LLM-generated overview of the issue and its
// comments, separating the comments into "old" and "new" groups broken
// by the specifed lastRead comment id. (The lastRead comment itself is
// considered "old", and must be present in the database).
// ForIssueUpdate does not make any requests to GitHub; the issue and comment data must already
// be stored in db.
func (c *Client) ForIssueUpdate(ctx context.Context, iss *github.Issue, lastRead int64) (*IssueUpdateResult, error) {
	post := iss.ToLLMDoc()
	var oldComments, newComments []*llmapp.Doc
	foundTarget := false
	for c := range c.gh.Comments(iss) {
		// New comment.
		if c.CommentID() > lastRead {
			newComments = append(newComments, c.ToLLMDoc())
			continue
		}
		if c.CommentID() == lastRead {
			foundTarget = true
		}
		oldComments = append(oldComments, c.ToLLMDoc())
	}
	if !foundTarget {
		return nil, fmt.Errorf("issue %d comment %d not found in database", iss.Number, lastRead)
	}
	overview, err := c.lc.UpdatedPostOverview(ctx, post, oldComments, newComments)
	if err != nil {
		return nil, err
	}
	return &IssueUpdateResult{
		NewComments:   len(newComments),
		TotalComments: len(oldComments) + len(newComments),
		Overview:      overview,
	}, nil
}

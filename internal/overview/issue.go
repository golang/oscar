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

type generator struct {
	gh      *github.Client
	lc      *llmapp.Client
	ignores []func(*github.IssueComment) bool // ignore these comments when generating overviews
}

func newGenerator(gh *github.Client, lc *llmapp.Client) *generator {
	return &generator{
		gh: gh,
		lc: lc,
	}
}

// skipCommentsBy configures the generator to ignore comments posted
// by the given GitHub user when generating issue overviews.
func (g *generator) skipCommentsBy(login string) {
	g.ignores = append(g.ignores, func(ic *github.IssueComment) bool {
		return ic.User.Login == login
	})
}

// IssueResult is the result of [Client.ForIssue].
// It contains the generated overview and metadata about the issue.
type IssueResult struct {
	TotalComments   int            // total number of comments for this issue
	LastComment     int64          // ID of the highest-numbered comment present for this issue
	SkippedComments int            // number of comments not included in the summary
	Overview        *llmapp.Result // the LLM-generated issue and comment summary
}

// See comment on [Client.ForIssue].
func (g *generator) issue(ctx context.Context, iss *github.Issue) (*IssueResult, error) {
	post := iss.ToLLMDoc()
	var cds []*llmapp.Doc
	m := g.newIssueMeta()
	for ic := range g.gh.Comments(iss) {
		if m.add(ic) {
			continue
		}
		cds = append(cds, ic.ToLLMDoc())
	}
	overview, err := g.lc.PostOverview(ctx, post, cds)
	if err != nil {
		return nil, err
	}
	return &IssueResult{
		TotalComments:   m.TotalComments,
		SkippedComments: m.SkippedComments,
		LastComment:     m.LastComment,
		Overview:        overview,
	}, nil
}

// ignore reports whether the given issue comment should be ignored
// when generating issue overviews.
func (g *generator) ignore(ic *github.IssueComment) bool {
	for _, ig := range g.ignores {
		if ig(ic) {
			return true
		}
	}
	return false
}

// IssueUpdateResult is the result of [Client.ForIssueUpdate].
// It contains the generated overview and metadata about the issue.
type IssueUpdateResult struct {
	TotalComments   int
	LastComment     int64
	SkippedComments int

	NewComments int            // number of new comments used in the summary
	Overview    *llmapp.Result // the LLM-generated issue and comment summary
}

// See comment on [Client.ForIssueUpdate].
func (g *generator) issueUpdate(ctx context.Context, iss *github.Issue, lastRead int64) (*IssueUpdateResult, error) {
	post := iss.ToLLMDoc()
	var oldComments, newComments []*llmapp.Doc
	foundTarget := false
	m := g.newIssueMeta()
	for ic := range g.gh.Comments(iss) {
		if ignore := m.add(ic); ignore {
			continue
		}
		// New comment.
		if ic.CommentID() > lastRead {
			newComments = append(newComments, ic.ToLLMDoc())
			continue
		}
		if ic.CommentID() == lastRead {
			foundTarget = true
		}
		oldComments = append(oldComments, ic.ToLLMDoc())
	}
	if !foundTarget {
		return nil, fmt.Errorf("issue %d comment %d not found in database", iss.Number, lastRead)
	}
	overview, err := g.lc.UpdatedPostOverview(ctx, post, oldComments, newComments)
	if err != nil {
		return nil, err
	}
	return &IssueUpdateResult{
		NewComments:     len(newComments),
		TotalComments:   m.TotalComments,
		SkippedComments: m.SkippedComments,
		LastComment:     m.LastComment,
		Overview:        overview,
	}, nil
}

// issueMeta contains metadata about a [github.Issue] that can't be
// determined from the issue itself.
type issueMeta struct {
	TotalComments   int   // total number of comments for this issue
	LastComment     int64 // ID of the highest-numbered comment present for this issue
	SkippedComments int   // number of ignored comments (by ignore)

	// comments to ignore (must be set before any calls to [issueMeta.add])
	ignore func(*github.IssueComment) bool
}

// newIssueMeta creates a new issueMeta with the same ignore
// function as the generator.
func (g *generator) newIssueMeta() *issueMeta {
	return &issueMeta{
		ignore: g.ignore,
	}
}

// add updates the issueMeta to include the given comment,
// and reports whether the comment should be ignored.
func (i *issueMeta) add(ic *github.IssueComment) (ignore bool) {
	i.TotalComments++
	if ic.CommentID() > i.LastComment {
		i.LastComment = ic.CommentID()
	}
	if i.ignore != nil && i.ignore(ic) {
		i.SkippedComments++
		return true
	}
	return false
}

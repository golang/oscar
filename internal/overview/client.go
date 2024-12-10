// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package overview generates and posts overviews of discussions.
// For now, it only works with GitHub issues and their comments.
// TODO(tatianabradley): Add comment explaining design.
package overview

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/llmapp"
	"golang.org/x/oscar/internal/storage"
	"rsc.io/ordered"
)

// A Client is used to generate, post, and update AI-generated overviews
// of GitHub issues and their comments.
type Client struct {
	slog                  *slog.Logger
	db                    storage.DB    // the database to use to store state
	minTimeBetweenUpdates time.Duration // the minimum time between calls to [poster.run]

	g *generator // for generating overviews
	p *poster    // for modifying GitHub
}

// New returns a new Client used to generate and post overviews to GitHub.
// Name is a string used to identify the Client, and bot is the login of the
// GitHub user that will modify GitHub.
// Clients with the same name and bot use the same state.
func New(lg *slog.Logger, db storage.DB, gh *github.Client, lc *llmapp.Client, name, bot string) *Client {
	c := &Client{
		slog:                  lg,
		db:                    db,
		minTimeBetweenUpdates: defaultMinTimeBetweenUpdates,
		g:                     newGenerator(gh, lc),
		p:                     newPoster(name, bot),
	}
	c.g.skipCommentsBy(bot)
	return c
}

var defaultMinTimeBetweenUpdates = 24 * time.Hour

// Run posts and updates AI-generated overviews of GitHub issues.
//
// TODO(tatianabradley): Detailed comment.
func (c *Client) Run(ctx context.Context) error {
	c.slog.Info("overview.Run start")
	defer func() {
		c.slog.Info("overview.Run end")
	}()

	// Check if we should run or not.
	c.db.Lock(string(c.runKey()))
	defer c.db.Unlock(string(c.runKey()))

	lr, err := c.lastRun()
	if err != nil {
		return err
	}
	if time.Since(lr) < c.minTimeBetweenUpdates {
		c.slog.Info("overview.Run: skipped (last successful run happened too recently)", "last run", lr, "min time", c.minTimeBetweenUpdates)
		return nil
	}
	if err := c.p.run(); err != nil {
		return err
	}

	c.setLastRun(time.Now())
	return nil
}

// ForIssue returns an LLM-generated overview of the issue and its comments.
// It does not make any requests to, or modify, GitHub; the issue and comment data must already
// be stored in the database.
func (c *Client) ForIssue(ctx context.Context, iss *github.Issue) (*IssueResult, error) {
	return c.g.issue(ctx, iss)
}

// ForIssueUpdate returns an LLM-generated overview of the issue and its
// comments, separating the comments into "old" and "new" groups broken
// by the specifed lastRead comment id. (The lastRead comment itself is
// considered "old", and must be present in the database).
//
// ForIssueUpdate does not make any requests to, or modify, GitHub; the issue and comment data must already
// be stored in db.
func (c *Client) ForIssueUpdate(ctx context.Context, iss *github.Issue, lastRead int64) (*IssueUpdateResult, error) {
	return c.g.issueUpdate(ctx, iss, lastRead)
}

// EnableProject enables the Client to post on and update issues in the given
// GitHub project (for example "golang/go").
func (c *Client) EnableProject(project string) {
	c.p.projects[project] = true
}

// RequireApproval configures the poster to require approval for all actions.
func (c *Client) RequireApproval() {
	c.p.requireApproval = true
}

// AutoApprove configures the Client to auto-approve all its actions.
func (c *Client) AutoApprove() {
	c.p.requireApproval = false
}

// SetMinComments sets the minimum number of comments a post needs to get an
// overview comment.
func (c *Client) SetMinComments(n int) {
	c.p.minComments = n
}

type state struct {
	LastRun string // the time of the last sucessful (non-skipped) call to [Client.Run]
}

// lastRun returns the time of the last successful (non-skipped)
// call to [Client.Run].
func (c *Client) lastRun() (time.Time, error) {
	b, ok := c.db.Get(c.runKey())
	if !ok {
		return time.Time{}, nil
	}
	var s state
	if err := json.Unmarshal(b, &s); err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, s.LastRun)
}

// setLastRun sets the time of the last successful (non-skipped)
// call to [Client.Run].
func (c *Client) setLastRun(t time.Time) {
	c.db.Set(c.runKey(), storage.JSON(&state{
		LastRun: t.Format(time.RFC3339),
	}))
}

// runKey returns the key to use to store the state for this Client.
func (c *Client) runKey() []byte {
	return ordered.Encode(runKind, c.p.name, c.p.bot)
}

const runKind = "overview.Run"

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package overview generates and posts overviews of discussions.
// For now, it only works with GitHub issues and their comments.
//
// Create a client to generate and post overviews via [New]. Use
// [Client.Issue] and [Client.IssueUpdate] to generate overviews.
// Call [Run] to log post/update actions for issues that need overviews,
// or updates to their overviews.
//
// Database entries are as follows:
//
//   - (overview.Run, $name, $bot) -> [runState]: holds state about calls to [Client.Run]
//   - (overview.IssueState, $name, $bot, $project, $issue) -> [issueState]: holds state about individual GitHub issues
//   - Watchers with name "overview.PostOrUpdate"+$name+$bot.
//   - Action log entries of kind "overview.Post" and "overview.Update".
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
	slog *slog.Logger
	db   storage.DB // the database to use to store state

	g *generator // for generating overviews
	p *poster    // for modifying GitHub
}

// New returns a new Client used to generate and post overviews to GitHub.
// Name is a string used to identify the Client, and bot is the login of the
// GitHub user that will modify GitHub.
// Clients with the same name and bot use the same state.
func New(lg *slog.Logger, db storage.DB, gh *github.Client, lc *llmapp.Client, name, bot string) *Client {
	c := &Client{
		slog: lg,
		db:   db,
		g:    newGenerator(gh, lc),
		p:    newPoster(lg, db, gh, name, bot),
	}
	c.g.skipCommentsBy(bot)
	return c
}

// the minimum time between calls to [poster.run]
var minTimeBetweenUpdates = 24 * time.Hour

// Run computes AI-generated overviews of GitHub issues, and adds
// appropriate actions (post or update) to the action log.
//
// Run is configured to only do work once every 24 hours, to avoid
// making too many LLM calls. If not enough time has passed since the
// last successful run, Run logs a message and returns.
//
// Run assumes that the underlying database of GitHub issues, comments and events is not
// being modified. (So, it must not be run in parallel with a GitHub sync.)
//
// See [poster.run] for additional implementation details.
func (c *Client) Run(ctx context.Context) error {
	return c.run(ctx, time.Now())
}

func (c *Client) run(ctx context.Context, now time.Time) error {
	c.slog.Info("overview.Run start")
	defer func() {
		c.slog.Info("overview.Run end")
	}()

	k := string(c.runKey())
	c.db.Lock(k)
	defer c.db.Unlock(k)

	// Check if we should run or not.
	lr, err := c.lastRun()
	if err != nil {
		return err
	}
	if now.Sub(lr) < minTimeBetweenUpdates {
		c.slog.Info("overview.Run: skipped (last successful run happened too recently)", "last run", lr, "min time", minTimeBetweenUpdates)
		return nil
	}
	if err := c.p.run(ctx, c.ForIssue, now); err != nil {
		return err
	}

	c.setLastRun(now)
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
	c.p.EnableProject(project)
}

// RequireApproval configures the Client to require approval for all actions.
func (c *Client) RequireApproval() {
	c.p.RequireApproval()
}

// AutoApprove configures the Client to auto-approve all its actions.
func (c *Client) AutoApprove() {
	c.p.AutoApprove()
}

// SetMinComments sets the minimum number of comments an issue needs to get an
// overview comment.
func (c *Client) SetMinComments(n int) {
	c.p.SetMinComments(n)
}

// SetMaxIssueAge sets the maximum age of an issue to get an overview comment.
func (c *Client) SetMaxIssueAge(age time.Duration) {
	c.p.SetMaxIssueAge(age)
}

// SkipIssueAuthor configures the Client to not post overview comments
// for issues by the given author.
func (c *Client) SkipIssueAuthor(author string) {
	c.p.SkipIssueAuthor(author)
}

// SkipCommentsBy configures the Client to always skip comments authored
// by the given GitHub user when generating overviews.
func (c *Client) SkipCommentsBy(user string) {
	c.g.skipCommentsBy(user)
}

type runState struct {
	LastRun string // the time the last sucessful (non-skipped) call to [Client.Run] began
}

// lastRun returns the time of the last successful (non-skipped)
// call to [Client.Run].
func (c *Client) lastRun() (time.Time, error) {
	b, ok := c.db.Get(c.runKey())
	if !ok {
		return time.Time{}, nil
	}
	var s runState
	if err := json.Unmarshal(b, &s); err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, s.LastRun)
}

// setLastRun sets the time of the last successful (non-skipped)
// call to [Client.Run].
func (c *Client) setLastRun(t time.Time) {
	c.db.Set(c.runKey(), storage.JSON(&runState{
		LastRun: t.Format(time.RFC3339),
	}))
}

// runKey returns the key to use to store the state for this Client.
func (c *Client) runKey() []byte {
	return ordered.Encode(runKind, c.p.name, c.p.bot)
}

const runKind = "overview.Run"

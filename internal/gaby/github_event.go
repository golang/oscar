// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"net/http"

	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/githubdocs"
)

// handleGitHubEvent handles incoming webhook requests from GitHub.
//
// If the incoming request was triggered by a new GitHub issue, it
// runs the same actions as are peformed by the /cron endpoint.
//
// Otherwise, it logs the event and takes no other action.
//
// handleGitHubEvent returns an error if the request is invalid, for example:
//   - the request cannot be verified to come from GitHub
//   - the event occurred in a GitHub project not supported by this Gaby
//     instance
//   - the event type is not supported by this implementation
func (g *Gaby) handleGitHubEvent(r *http.Request) error {
	event, err := github.ValidateWebhookRequest(r, g.githubProject, g.secret)
	if err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}

	switch p := event.Payload.(type) {
	case *github.WebhookIssueEvent:
		return g.handleGithubIssueEvent(r.Context(), p)
	default:
		g.slog.Info("ignoring new non-issue GitHub event", "event", event)
	}

	return nil
}

// handleGitHubIssueEvent handles incoming GitHub "issue" events.
//
// If the event is a new issue, the function syncs
// the corresponding GitHub project, posts related issues for and fixes
// comments on the new GitHub issue.
//
// Otherwise, it logs the event and takes no other action.
func (g *Gaby) handleGithubIssueEvent(ctx context.Context, event *github.WebhookIssueEvent) error {
	if event.Action != github.WebhookIssueActionOpened {
		g.slog.Info("ignoring GitHub issue event (action is not opened)", "event", event, "action", event)
		return nil
	}

	g.slog.Info("handling GitHub issue event", "event", event)

	project := event.Repository.Project
	if flags.enablesync {
		if err := g.syncGitHubProject(ctx, project); err != nil {
			return err
		}
		if err := g.embedAll(ctx); err != nil {
			return err
		}
	}

	// Do not attempt changes unless sync is enabled and completely succeeded.
	if flags.enablechanges && flags.enablesync {
		// No need to lock; [related.Poster.Post] and [related.Poster.Run] can
		// happen concurrently.
		if err := g.relatedPoster.Post(ctx, project, event.Issue.Number); err != nil {
			return err
		}
		// No need to lock; [commentfix.Fixer.FixGitHubIssue] and
		// [commentfix.Fixer.Run] can happen concurrently.
		if err := g.commentFixer.FixGitHubIssue(ctx, project, event.Issue.Number); err != nil {
			return err
		}
	}

	return nil
}

// syncGitHubProject syncs the document database with respect to a single
// GitHub project.
func (g *Gaby) syncGitHubProject(ctx context.Context, project string) error {
	g.db.Lock(gabyGitHubSyncLock)
	defer g.db.Unlock(gabyGitHubSyncLock)

	if err := g.github.SyncProject(ctx, project); err != nil {
		return err
	}
	return githubdocs.Sync(ctx, g.slog, g.docs, g.github)
}

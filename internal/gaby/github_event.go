// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"golang.org/x/oscar/internal/actions"
	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/github"
)

// handleGitHubEvent handles incoming webhook requests from GitHub
// and reports whether the request was handled.
//
// If the incoming request was triggered by supported event, and sync
// is enabled, it syncs its relevant state. If changes are enabled,
// it takes relevant actions in response to the event.
//
// Otherwise, it logs the event and returns (false, nil).
//
// The supported events are:
//   - new GitHub issue (see [Gaby.handleGitHubIssueEvent])
//   - new GitHub issue comment (see [Gaby.handleGitHubIssueCommentEvent])
//
// handled is true if all appropriate syncs and actions were performed
// in response to the event, and false if the event was skipped or an
// error occurred. (handled is also false if either of [gabyFlags.enablesync]
// or [gabyFlags.enablechanges] is false.)
//
// handleGitHubEvent returns an error if any of the syncs or actions fails,
// of if the webhook request is invalid according to [github.ValidateWebhookRequest].
func (g *Gaby) handleGitHubEvent(r *http.Request, fl *gabyFlags) (handled bool, err error) {
	event, err := github.ValidateWebhookRequest(r, g.secret)
	if err != nil {
		return false, fmt.Errorf("%w: %v", errInvalidWebhookRequest, err)
	}

	if event.Project() != g.githubProject {
		g.slog.Warn("unexpected webhook request", "webhook_project", event.Project(), "gaby_project", g.githubProject, "event", event)
		return false, nil
	}

	switch p := event.Payload.(type) {
	case *github.WebhookIssueEvent:
		return g.handleGitHubIssueEvent(r.Context(), p, fl)
	case *github.WebhookIssueCommentEvent:
		return g.handleGitHubIssueCommentEvent(r.Context(), p, fl)
	default:
		g.slog.Info("ignoring GitHub event", "type", event.Type, "event", event)
	}

	return false, nil
}

var errInvalidWebhookRequest = errors.New("invalid webhook request")

// handleGitHubIssueEvent handles an incoming GitHub "issue" event and
// reports whether the event was handled.
//
// If the event is a new issue, and sync is enabled, the function
// syncs the corresponding GitHub project. If changes are also enabled,
// it posts related issues and fixes the body and comments of the issue.
//
// It returns an error immediately if any of the syncs or actions fails.
//
// Otherwise, it logs the event and returns (false, nil).
func (g *Gaby) handleGitHubIssueEvent(ctx context.Context, event *github.WebhookIssueEvent, fl *gabyFlags) (handled bool, _ error) {
	if event.Action != github.WebhookIssueActionOpened {
		g.slog.Info("ignoring GitHub issue event (action is not opened)", "event", event, "action", event.Action)
		return false, nil
	}

	g.slog.Info("handling GitHub issue", "event", event)

	project := event.Repository.Project
	if fl.enablesync {
		if err := g.syncGitHubProject(ctx, project); err != nil {
			return false, err
		}
		if err := g.embedAll(ctx); err != nil {
			return false, err
		}
	}

	// Do not attempt changes unless sync is enabled and completely succeeded.
	if fl.enablechanges && fl.enablesync {
		// No need to lock; [related.Poster.Post] and [related.Poster.Run] can
		// happen concurrently.
		if err := g.relatedPoster.Post(ctx, project, event.Issue.Number); err != nil {
			return false, err
		}
		if err := g.fixGitHubIssue(ctx, project, event.Issue.Number); err != nil {
			return false, err
		}
		return true, nil
	}

	return false, nil
}

// handleGitHubIssueCommentEvent handles an incoming GitHub "issue comment" event
// and reports whether the event was handled.
//
// If the event is a new issue comment, and sync is enabled, the function
// syncs the corresponding GitHub project. If changes are also enabled,
// it fixes the body and comments of the issue to which the comment
// was posted.
//
// It returns an error immediately if any of the syncs or actions fails.
//
// Otherwise, it logs the event and returns (false, nil).
func (g *Gaby) handleGitHubIssueCommentEvent(ctx context.Context, event *github.WebhookIssueCommentEvent, fl *gabyFlags) (handled bool, _ error) {
	if event.Action != github.WebhookIssueCommentActionCreated {
		g.slog.Info("ignoring GitHub issue comment event (action is not created)", "event", event, "action", event.Action)
		return false, nil
	}

	g.slog.Info("handling GitHub issue comment", "event", event)

	project := event.Repository.Project
	if fl.enablesync {
		if err := g.syncGitHubProject(ctx, project); err != nil {
			return false, err
		}
		// Embeddings are not needed to apply fixes.
	}

	// Do not attempt changes unless sync is enabled and completely succeeded.
	if fl.enablechanges && fl.enablesync {
		if err := g.fixGitHubIssue(ctx, project, event.Issue.Number); err != nil {
			return false, err
		}
		return true, nil
	}

	return false, nil
}

func (g *Gaby) fixGitHubIssue(ctx context.Context, project string, issue int64) error {
	// No need to lock; [commentfix.Fixer.FixGitHubIssue] and
	// [commentfix.Fixer.Run] can happen concurrently.
	if err := g.commentFixer.LogFixGitHubIssue(ctx, project, issue); err != nil {
		return err
	}
	if err := actions.Run(ctx, g.slog, g.db); err != nil {
		return err
	}
	return nil
}

// syncGitHubProject syncs the document corpus with respect to a single
// GitHub project.
func (g *Gaby) syncGitHubProject(ctx context.Context, project string) error {
	g.db.Lock(gabyGitHubSyncLock)
	defer g.db.Unlock(gabyGitHubSyncLock)

	if err := g.github.SyncProject(ctx, project); err != nil {
		return err
	}
	docs.Sync(g.docs, g.github)
	return nil
}

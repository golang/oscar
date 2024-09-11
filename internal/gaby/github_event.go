// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"golang.org/x/oscar/internal/github"
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
// If the event corresponds to a new issue, the function runs the
// same actions as are peformed by the /cron endpoint.
//
// Otherwise, it logs the event and takes no other action.
func (g *Gaby) handleGithubIssueEvent(ctx context.Context, event *github.WebhookIssueEvent) error {
	if event.Action != github.WebhookIssueActionOpened {
		g.slog.Info("ignoring GitHub issue event (action is not opened)", "event", event, "action", event)
	}

	g.slog.Info("handling GitHub issue event", "event", event)
	return errors.Join(g.syncAndRunAll(ctx)...)
}

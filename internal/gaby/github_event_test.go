// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"log/slog"
	"testing"

	"golang.org/x/oscar/internal/github"
)

func TestHandleGitHubEvent(t *testing.T) {
	t.Run("valid new issue runs action", func(t *testing.T) {
		validPayload := &github.WebhookIssueEvent{
			Action: github.WebhookIssueActionOpened,
			Repository: github.Repository{
				Project: "a/project",
			},
		}
		r, db := github.ValidWebhookTestdata(t, "issues", validPayload)
		g := &Gaby{githubProject: "a/project", secret: db, slog: slog.Default()}
		if err := g.handleGitHubEvent(r); err != nil {
			t.Fatalf("handleGitHubEvent err = %v, want nil", err)
		}
	})

	// Valid event that we don't handle yet.
	t.Run("valid issue comment ignored", func(t *testing.T) {
		validPayload := &github.WebhookIssueCommentEvent{
			Action: github.WebhookIssueCommentActionCreated,
			Repository: github.Repository{
				Project: "a/project",
			},
		}
		r, db := github.ValidWebhookTestdata(t, "issue_comment", validPayload)
		g := &Gaby{githubProject: "a/project", secret: db, slog: slog.Default()}
		if err := g.handleGitHubEvent(r); err != nil {
			t.Fatalf("handleGitHubEvent err = %v, want nil", err)
		}
	})

	t.Run("error wrong project", func(t *testing.T) {
		validPayload := &github.WebhookIssueEvent{
			Action: github.WebhookIssueActionOpened,
			Repository: github.Repository{
				Project: "wrong/project",
			},
		}
		r, db := github.ValidWebhookTestdata(t, "issues", validPayload)
		g := &Gaby{githubProject: "a/project", secret: db, slog: slog.Default()}
		if err := g.handleGitHubEvent(r); err == nil {
			t.Fatal("handleGitHubEvent err = nil, want err")
		}
	})
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"log/slog"
	"testing"

	"golang.org/x/oscar/internal/github"
)

func TestHandleGitHubEvent(t *testing.T) {
	t.Run("valid new issue runs actions", func(t *testing.T) {
		validPayload := &github.WebhookIssueEvent{
			Action: github.WebhookIssueActionOpened,
			Repository: github.Repository{
				Project: "a/project",
			},
		}
		r, db := github.ValidWebhookTestdata(t, "issues", validPayload)
		ran := false
		actions := []func(context.Context) error{
			func(context.Context) error {
				ran = true
				return nil
			},
		}
		g := &Gaby{githubProject: "a/project", secret: db, slog: slog.Default(), actions: actions}
		if err := g.handleGitHubEvent(r); err != nil {
			t.Fatalf("handleGitHubEvent err = %v, want nil", err)
		}
		if !ran {
			t.Errorf("handleGitHubEvent actions did not run")
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
		ran := false
		actions := []func(context.Context) error{
			func(context.Context) error {
				ran = true
				return nil
			},
		}
		g := &Gaby{githubProject: "a/project", secret: db, slog: slog.Default(), actions: actions}
		if err := g.handleGitHubEvent(r); err != nil {
			t.Fatalf("handleGitHubEvent err = %v, want nil", err)
		}
		if ran {
			t.Errorf("handleGitHubEvent ran actions unexpectedley")
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
		ran := false
		actions := []func(context.Context) error{
			func(context.Context) error {
				ran = true
				return nil
			},
		}
		g := &Gaby{githubProject: "a/project", secret: db, slog: slog.Default(), actions: actions}
		if err := g.handleGitHubEvent(r); err == nil {
			t.Fatal("handleGitHubEvent err = nil, want err")
		}
		if ran {
			t.Errorf("handleGitHubEvent ran actions unexpectedley")
		}
	})
}

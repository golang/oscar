// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/oscar/internal/commentfix"
	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/httprr"
	"golang.org/x/oscar/internal/labels"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/related"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

const (
	testProject  = "rsc/tmp"
	testProject2 = "rsc/markdown"
)

func TestHandleGitHubEvent(t *testing.T) {
	fl := &gabyFlags{
		enablechanges: true,
		enablesync:    true,
	}

	for _, tc := range []struct {
		name        string
		payload     any
		payloadType github.WebhookEventType
		wantHandled bool
		wantErr     error
	}{
		{
			// Newly opened issues are handled.
			name: "new issue",
			payload: &github.WebhookIssueEvent{
				Action: github.WebhookIssueActionOpened,
				Issue: github.Issue{
					Number: 4,
				},
				Repository: github.Repository{
					Project: testProject,
				},
			},
			payloadType: "issues",
			wantHandled: true,
		},
		{
			// Reopened issues are ignored.
			name: "reopened issue",
			payload: &github.WebhookIssueEvent{
				Action: github.WebhookIssueAction("reopened"),
				Repository: github.Repository{
					Project: testProject,
				},
			},
			payloadType: "issues",
			wantHandled: false,
		},
		{
			// New issue comments are handled.
			name: "new issue comment",
			payload: &github.WebhookIssueCommentEvent{
				Action: github.WebhookIssueCommentActionCreated,
				Issue: github.Issue{
					Number: 4,
				},
				Repository: github.Repository{
					Project: testProject,
				},
			},
			payloadType: github.WebhookEventTypeIssueComment,
			wantHandled: true,
		},
		{
			// Second project is handled too.
			name: "new issue comment2",
			payload: &github.WebhookIssueCommentEvent{
				Action: github.WebhookIssueCommentActionCreated,
				Issue: github.Issue{
					Number: 3,
				},
				Repository: github.Repository{
					Project: testProject2,
				},
			},
			payloadType: github.WebhookEventTypeIssueComment,
			wantHandled: true,
		},
		{
			// Incorrect project skips the event but doesn't return an error.
			name: "wrong project",
			payload: &github.WebhookIssueEvent{
				Action: github.WebhookIssueActionOpened,
				Repository: github.Repository{
					Project: "wrong/project",
				},
			},
			payloadType: github.WebhookEventTypeIssue,
			wantHandled: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, secret := github.ValidWebhookTestdata(t, tc.payloadType, tc.payload)
			g := testGaby(t, secret)
			handled, err := g.handleGitHubEvent(r, fl)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("handleGitHubEvent err = %v, want %v", err, tc.wantErr)
			}
			if handled != tc.wantHandled {
				t.Errorf("handleGitHubEvent handled = %t, want %t", handled, tc.wantHandled)
			}
		})
	}
}

// testGaby returns a Gaby instance for testing the GitHub webhook.
// secret should contain a secret for validating the webhook response.
func testGaby(t *testing.T, secret secret.DB) *Gaby {
	t.Helper()
	check := testutil.Checker(t)

	lg := testutil.Slogger(t)
	db := storage.MemDB()
	dc := docs.New(lg, db)

	gh := testGHClient(t, check, lg, db)

	vdb := storage.MemVectorDB(db, lg, "vecs")
	emb := llm.QuoteEmbedder()
	cgen := llm.EchoContentGenerator()

	rp := related.New(lg, db, gh, vdb, dc, "related")
	rp.EnableProject(testProject)
	rp.EnableProject(testProject2)
	rp.EnablePosts()

	cf := commentfix.New(lg, gh, db, "fix")
	cf.EnableProject(testProject)
	cf.EnableProject(testProject2)
	// No fixes yet.
	cf.EnableEdits()

	lab := labels.New(lg, db, gh, cgen, "labels")

	return &Gaby{
		githubProjects: []string{testProject, testProject2},
		github:         gh,
		vector:         vdb,
		secret:         secret,
		db:             db,
		slog:           lg,
		embed:          emb,
		docs:           dc,
		commentFixer:   cf,
		relatedPoster:  rp,
		labeler:        lab,
	}
}

func testGHClient(t *testing.T, check func(error), lg *slog.Logger, db storage.DB) *github.Client {
	t.Helper()

	fname := fmt.Sprintf("testdata/webhook/%s.httprr", t.Name())
	if _, err := os.Stat(fname); err != nil {
		dir := filepath.Dir(fname)
		check(os.MkdirAll(dir, os.ModePerm))
		_, err := os.Create(fname)
		check(err)
	}

	rr, err := httprr.Open(fname, http.DefaultTransport)
	check(err)
	rr.ScrubReq(github.Scrub)
	sdb := secret.DB(secret.Map{"api.github.com": "user:pass"})
	if rr.Recording() {
		sdb = secret.Netrc()
	}
	c := github.New(lg, db, sdb, rr.Client())
	check(c.Add(testProject))
	check(c.Add(testProject2))

	return c
}

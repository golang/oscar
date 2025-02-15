// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package goreviews

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"
	"sync"

	"golang.org/x/oscar/internal/gerrit"
	"golang.org/x/oscar/internal/reviews"
)

// Changes manages the data held for changes to display on the dashboard.
type Changes struct {
	slog     *slog.Logger
	client   *gerrit.Client
	projects []string

	mu  sync.Mutex // protects cps
	cps []reviews.ChangePreds
}

// New returns a new Changes to manage the Go dashboard.
func New(lg *slog.Logger, client *gerrit.Client, projects []string) *Changes {
	return &Changes{
		slog:     lg,
		client:   client,
		projects: projects,
	}
}

// Sync updates and re-scores the list of changes we will display.
// TODO: Add some sort of incremental sync, rather than reading
// all the data.
func (ch *Changes) Sync(ctx context.Context) error {
	cps, err := collectChanges(ctx, ch.slog, ch.client, ch.projects)
	if err != nil {
		return err
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.cps = cps

	return nil
}

// displayDoc is displayed on the dashboard.
const displayDoc template.HTML = `Excluding those marked WIP, having hashtags "wait-author", "wait-release", "wait-issue", or description containing "DO NOT REVIEW".`

// Display is an HTTP handler that displays the dashboard.
func (ch *Changes) Display(endpoint string, w http.ResponseWriter, r *http.Request) {
	ch.mu.Lock()
	cps := ch.cps
	ch.mu.Unlock()

	reviews.Display(ch.slog, displayDoc, endpoint, cps, w, r)
}

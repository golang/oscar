// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package goreviews

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

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
func (ch *Changes) Display(ctx context.Context, endpoint string, w http.ResponseWriter, r *http.Request) {
	ch.mu.Lock()
	cps := ch.cps
	ch.mu.Unlock()

	categories := strings.ReplaceAll(categoriesJSON, "$ONEYEARAGO", time.Now().AddDate(-1, 0, 0).Format(`\"2006-01-02\"`))

	reviews.Display(ctx, ch.slog, displayDoc, categories, endpoint, cps, w, r)
}

// categoriesJSON is the default list of ways to categorize issues
// when displaying them. This is decoded into a []reviews.categoryDef.
// The string $ONEYEARAGO is replaced before use.
//
// Issues are matched against these categories in order, and stop at
// the first match. That is, an issue that matches "Ready with Comments"
// will not be compared against "Ready with Merge Conflict".
// Therefore, the filters do not specifically exclude cases that are
// handled by earlier categories.
const categoriesJSON = `
[
  {
    "name": "Ready",
    "doc": "approved and ready to submit",
    "filter": "Predicates.Name:hasPlusTwo AND Predicates.Name:trybotsPassed AND NOT Predicates.Name:hasUnresolvedComments AND NOT Predicates.Name:mergeConflict"
  },
  {
    "name": "Ready with Comments",
    "doc": "approved but has unresolved comments",
    "filter": "Predicates.Name:hasPlusTwo AND Predicates.Name:trybotsPassed AND NOT Predicates.Name:mergeConflict"
  },
  {
    "name": "Ready with Merge Conflict",
    "doc": "approved but has a merge conflict",
    "filter": "Predicates.Name:hasPlusTwo AND Predicates.Name:trybotsPassed"
  },
  {
    "name": "From Maintainer",
    "doc": "from a maintainer",
    "filter": "Predicates.Name:authorMaintainer AND Change.Created>$ONEYEARAGO"
  },
  {
    "name": "Older From Maintainer",
    "doc": "from a maintainer, but created more than a year ago",
    "filter": "Predicates.Name:authorMaintainer"
  },
  {
    "name": "From Major Contributor",
    "doc": "from a major contributor",
    "filter": "Predicates.Name:authorMajorContributor AND Change.Created>$ONEYEARAGO"
  },
  {
    "name": "Older From Major Contributor",
    "doc": "from a major contributor, but created more than a year ago",
    "filter": "Predicates.Name:authorMajorContributor"
  }
]
`

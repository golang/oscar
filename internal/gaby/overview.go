// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/safehtml"
	"github.com/google/safehtml/template"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/htmlutil"
)

// overviewPage holds the fields needed to display the results
// of a search.
type overviewPage struct {
	overviewForm // the raw form inputs
	Result       *overviewResult
	Error        string // if non-empty, the error to display instead of the result
}

type overviewResult struct {
	github.IssueOverviewResult               // the raw result
	OverviewHTML               safehtml.HTML // the overview as HTML
}

// overviewForm holds the raw inputs to the overview form.
type overviewForm struct {
	Query string // the issue ID to lookup
}

func (g *Gaby) handleOverview(w http.ResponseWriter, r *http.Request) {
	handlePage(w, g.populateOverviewPage(r), overviewPageTmpl)
}

var overviewPageTmpl = newTemplate(overviewPageTmplFile, template.FuncMap{
	"fmttime": fmtTimeString,
})

// fmtTimeString formats an [time.RFC3339]-encoded time string
// as a [time.DateOnly] time string.
func fmtTimeString(s string) string {
	if s == "" {
		return s
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.Format(time.DateOnly)
}

// populateOverviewPage returns the contents of the overview page.
func (g *Gaby) populateOverviewPage(r *http.Request) overviewPage {
	form := overviewForm{
		Query: r.FormValue("q"),
	}
	if form.Query == "" {
		return overviewPage{
			overviewForm: form,
		}
	}
	issue, err := strconv.Atoi(strings.TrimSpace(form.Query))
	if err != nil {
		return overviewPage{
			overviewForm: form,
			Error:        fmt.Errorf("invalid form value %q: %w", form.Query, err).Error(),
		}
	}
	overview, err := github.IssueOverview(r.Context(), g.generate, g.db, g.githubProject, int64(issue))
	if err != nil {
		return overviewPage{
			overviewForm: form,
			Error:        fmt.Errorf("overview: %w", err).Error(),
		}
	}
	return overviewPage{
		overviewForm: form,
		Result: &overviewResult{
			IssueOverviewResult: *overview,
			OverviewHTML:        htmlutil.MarkdownToSafeHTML(overview.Overview.Overview),
		},
	}
}

// Related returns the relative URL of the related-entity search
// for the issue.
func (r *overviewResult) Related() string {
	return fmt.Sprintf("/search?q=%s", r.Issue.HTMLURL)
}

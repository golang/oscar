// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/safehtml/template"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/htmlutil"
	"golang.org/x/oscar/internal/search"
)

// overviewPage holds the fields needed to display the results
// of a search.
type overviewPage struct {
	Form   overviewForm // the raw form inputs
	Result *overviewResult
	Error  error // if non-nil, the error to display instead of the result
}

type overviewResult struct {
	github.IssueOverviewResult        // the raw result
	Type                       string // the type of overview
}

// overviewForm holds the raw inputs to the overview form.
type overviewForm struct {
	Query        string // the issue ID to lookup
	OverviewType string // the type of overview to generate
}

// the possible overview types
const (
	issueOverviewType   = "issue"
	relatedOverviewType = "related"
)

// IsIssueOverview reports whether this overview result
// is of type [issueOverviewType].
func (r *overviewResult) IsIssueOverview() bool {
	return r.Type == issueOverviewType
}

// CheckRadio reports whether radio button with the given id
// should be checked.
func (p overviewPage) CheckRadio(id string) bool {
	// checked returns the id of the radio button that should be checked.
	checked := func() string {
		// If there is no result yet, the default option
		// (issue overview) should be checked.
		if p.Result == nil {
			return issueOverviewType
		}
		// Otherwise, the button corresponding to the result
		// type should be checked.
		return p.Result.Type
	}
	return id == checked()
}

func (g *Gaby) handleOverview(w http.ResponseWriter, r *http.Request) {
	handlePage(w, g.populateOverviewPage(r), overviewPageTmpl)
}

var overviewPageTmpl = newTemplate(overviewPageTmplFile, template.FuncMap{
	"fmttime":  fmtTimeString,
	"safehtml": htmlutil.MarkdownToSafeHTML,
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
	p := overviewPage{
		Form: overviewForm{
			Query:        r.FormValue("q"),
			OverviewType: r.FormValue("t"),
		},
	}
	q := strings.TrimSpace(p.Form.Query)
	if q == "" {
		return p
	}
	issue, err := strconv.ParseInt(q, 10, 64)
	if err != nil {
		p.Error = fmt.Errorf("invalid form value %q: %w", q, err)
		return p
	}
	if issue < 0 {
		p.Error = fmt.Errorf("invalid form value %q", q)
		return p
	}
	overview, err := g.overview(r.Context(), issue, p.Form.OverviewType)
	if err != nil {
		p.Error = err
		return p
	}
	p.Result = overview
	return p
}

// overview generates an overview of the issue of the given type.
func (g *Gaby) overview(ctx context.Context, issue int64, overviewType string) (*overviewResult, error) {
	switch overviewType {
	case "", issueOverviewType:
		return g.issueOverview(ctx, issue)
	case relatedOverviewType:
		return g.relatedOverview(ctx, issue)
	default:
		return nil, fmt.Errorf("unknown overview type %q", overviewType)
	}
}

// issueOverview generates an overview of the issue and its comments.
func (g *Gaby) issueOverview(ctx context.Context, issue int64) (*overviewResult, error) {
	overview, err := github.IssueOverview(ctx, g.llm, g.db, g.githubProject, issue)
	if err != nil {
		return nil, err
	}
	return &overviewResult{
		IssueOverviewResult: *overview,
		Type:                issueOverviewType,
	}, nil
}

// relatedOverview generates an overview of the issue and its related documents.
func (g *Gaby) relatedOverview(ctx context.Context, issue int64) (*overviewResult, error) {
	iss, err := github.LookupIssue(g.db, g.githubProject, issue)
	if err != nil {
		return nil, err
	}
	overview, err := search.Overview(ctx, g.llm, g.vector, g.docs, iss.DocID())
	if err != nil {
		return nil, err
	}
	return &overviewResult{
		IssueOverviewResult: github.IssueOverviewResult{
			Issue: iss,
			// number of comments not displayed for related type
			Overview: overview.OverviewResult,
		},
		Type: relatedOverviewType,
	}, nil
}

// Related returns the relative URL of the related-entity search
// for the issue.
func (r *overviewResult) Related() string {
	return fmt.Sprintf("/search?q=%s", r.Issue.HTMLURL)
}

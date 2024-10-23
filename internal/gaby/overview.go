// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/oscar/internal/github"
)

// overviewPage holds the fields needed to display the results
// of a search.
type overviewPage struct {
	overviewForm                            // the raw form inputs
	Result       github.IssueOverviewResult // the overview result to display
	Error        string                     // if non-empty, the error to display instead of the result
}

// overviewForm holds the raw inputs to the overview form.
type overviewForm struct {
	Query string // the issue ID to lookup
}

func (g *Gaby) handleOverview(w http.ResponseWriter, r *http.Request) {
	handlePage(w, g.populateOverviewPage(r), overviewPageTmpl)
}

var overviewPageTmpl = newTemplate(overviewPageTmplFile, nil)

// populateOverviewPage returns the contents of the overview page.
func (g *Gaby) populateOverviewPage(r *http.Request) overviewPage {
	form := overviewForm{
		Query: r.FormValue("q"),
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
	// TODO(tatianabradley): Convert markdown response to HTML.
	return overviewPage{
		overviewForm: form,
		Result:       *overview,
	}
}

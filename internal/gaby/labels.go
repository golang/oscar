// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/google/safehtml"
	"github.com/google/safehtml/template"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/htmlutil"
	"golang.org/x/oscar/internal/labels"
)

// labelsPage holds the fields needed to display the results
// of an issue categorization.
type labelsPage struct {
	CommonPage

	Params  labelsParams // the raw parameters
	Results []*labelsResult
	Error   error // if non-nil, the error to display instead of the result
}

type labelsResult struct {
	*github.Issue // the issue we're reporting on
	Category      labels.Category
	Explanation   string
	BodyHTML      safehtml.HTML
}

// labelsParams holds the raw inputs to the labels form.
type labelsParams struct {
	Query string // the issue ID to lookup
}

func (g *Gaby) handleLabels(w http.ResponseWriter, r *http.Request) {
	handlePage(w, g.populateLabelsPage(r), labelsPageTmpl)
}

var labelsPageTmpl = newTemplate(labelsPageTmplFile, template.FuncMap{})

// populateLabelsPage returns the contents of the labels page.
func (g *Gaby) populateLabelsPage(r *http.Request) *labelsPage {
	pm := labelsParams{
		Query: r.FormValue(paramQuery),
	}
	p := &labelsPage{
		Params: pm,
	}
	p.setCommonPage()
	if pm.Query == "" {
		return p
	}

	var project string
	if len(g.githubProjects) > 0 {
		project = g.githubProjects[0] // default to first project
	}
	var issueMin, issueMax int64
	smin, smax, ok := strings.Cut(pm.Query, ",")
	if ok {
		var err1, err2 error
		issueMin, err1 = strconv.ParseInt(smin, 10, 64)
		issueMax, err2 = strconv.ParseInt(smax, 10, 64)
		if err := errors.Join(err1, err2); err != nil {
			p.Error = err
			return p
		}
	} else {
		proj, issue, err := parseIssueNumber(pm.Query)
		if err != nil {
			p.Error = fmt.Errorf("invalid form value %q: %w", pm.Query, err)
			return p
		}
		if proj != "" {
			if !slices.Contains(g.githubProjects, proj) {
				p.Error = fmt.Errorf("invalid form value (unrecognized project): %q", pm.Query)
				return p
			}
			project = proj
		}
		issueMin = issue
		issueMax = issue
	}

	// Find issues in database.
	for i := range github.LookupIssues(g.db, project, issueMin, issueMax) {
		cat, exp, err := labels.IssueCategory(r.Context(), g.llm, i)
		if err != nil {
			p.Error = err
			return p
		}
		p.Results = append(p.Results, &labelsResult{
			Issue:       i,
			Category:    cat,
			Explanation: exp,
			BodyHTML:    htmlutil.MarkdownToSafeHTML(i.Body),
		})
	}
	return p
}

func (p *labelsPage) setCommonPage() {
	p.CommonPage = CommonPage{
		ID:          labelsID,
		Description: "Categorize issues.",
		Styles:      []safeURL{searchID.CSS()},
		Form: Form{
			Inputs:     p.Params.inputs(),
			SubmitText: "categorize",
		},
	}
}

func (pm *labelsParams) inputs() []FormInput {
	return []FormInput{
		{
			Label:       "issue",
			Type:        "int, int,int or string",
			Description: "the issue(s) to check, as a number, two numbers, or URL (e.g. 1234, golang/go#1234, or https://github.com/golang/go/issues/1234)",
			Name:        safeQuery,
			Required:    true,
			Typed: TextInput{
				ID:    safeQuery,
				Value: pm.Query,
			},
		},
	}
}

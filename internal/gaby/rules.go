// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/google/safehtml"
	"github.com/google/safehtml/template"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/htmlutil"
	"golang.org/x/oscar/internal/rules"
)

// rulesPage holds the fields needed to display the results
// of an issue rule check.
type rulesPage struct {
	CommonPage

	Params rulesParams // the raw parameters
	Result *rulesResult
	Error  error // if non-nil, the error to display instead of the result
}

type rulesResult struct {
	*github.Issue                   // the issue we're reporting on
	rules.IssueResult               // the raw result
	HTML              safehtml.HTML // issue response as HTML
}

// rulesParams holds the raw inputs to the rules form.
type rulesParams struct {
	Query string // the issue ID to lookup
}

func (g *Gaby) handleRules(w http.ResponseWriter, r *http.Request) {
	handlePage(w, g.populateRulesPage(r), rulesPageTmpl)
}

var rulesPageTmpl = newTemplate(rulesPageTmplFile, template.FuncMap{})

// populateRulesPage returns the contents of the rules page.
func (g *Gaby) populateRulesPage(r *http.Request) *rulesPage {
	pm := rulesParams{
		Query: r.FormValue(paramQuery),
	}
	p := &rulesPage{
		Params: pm,
	}
	p.setCommonPage()
	if pm.Query == "" {
		return p
	}
	proj, issue, err := parseIssueNumber(pm.Query)
	if err != nil {
		p.Error = fmt.Errorf("invalid form value %q: %w", pm.Query, err)
		return p
	}
	if proj == "" && len(g.githubProjects) > 0 {
		proj = g.githubProjects[0] // default to first project
	}
	if !slices.Contains(g.githubProjects, proj) {
		p.Error = fmt.Errorf("invalid form value (unrecognized project): %q", pm.Query)
		return p
	}
	if issue <= 0 {
		return p
	}
	// Find issue in database.
	i, err := github.LookupIssue(g.db, proj, issue)
	if err != nil {
		p.Error = fmt.Errorf("error looking up issue %q: %w", pm.Query, err)
		return p
	}

	rules, err := rules.Issue(r.Context(), g.db, g.llm, i, true)
	if err != nil {
		p.Error = err
		return p
	}
	p.Result = &rulesResult{
		Issue:       i,
		IssueResult: *rules,
		HTML:        htmlutil.MarkdownToSafeHTML(rules.Response),
	}
	return p
}

func (p *rulesPage) setCommonPage() {
	p.CommonPage = CommonPage{
		ID:          rulesID,
		Description: "Generate a list of rule violations for submitted golang/go issues.",
		Styles:      []safeURL{searchID.CSS()},
		Form: Form{
			Inputs:     p.Params.inputs(),
			SubmitText: "generate",
		},
	}
}

func (pm *rulesParams) inputs() []FormInput {
	return []FormInput{
		{

			Label:       "issue",
			Type:        "int or string",
			Description: "the issue to check, as a number or URL (e.g. 1234, golang/go#1234, or https://github.com/golang/go/issues/1234)",
			Name:        safeQuery,
			Required:    true,
			Typed: TextInput{
				ID:    safeQuery,
				Value: pm.Query,
			},
		},
	}
}

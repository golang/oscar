// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"net/http"

	"github.com/google/safehtml"
	"github.com/google/safehtml/template"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/htmlutil"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/rules"
)

// rulesPage holds the fields needed to display the results
// of an issue rule check.
type rulesPage struct {
	rulesForm // the raw form inputs
	Result    *rulesResult
	Error     string // if non-empty, the error to display instead of the result
}

type rulesResult struct {
	*github.Issue                   // the issue we're reporting on
	rules.IssueResult               // the raw result
	HTML              safehtml.HTML // issue response as HTML
}

// rulesForm holds the raw inputs to the rules form.
type rulesForm struct {
	Query string // the issue ID to lookup
}

func (g *Gaby) handleRules(w http.ResponseWriter, r *http.Request) {
	handlePage(w, g.populateRulesPage(r), rulesPageTmpl)
}

var rulesPageTmpl = newTemplate(rulesPageTmplFile, template.FuncMap{})

// populateRulesPage returns the contents of the rules page.
func (g *Gaby) populateRulesPage(r *http.Request) rulesPage {
	form := rulesForm{
		Query: r.FormValue("q"),
	}
	p := rulesPage{
		rulesForm: form,
	}
	if form.Query == "" {
		return p
	}
	proj, issue, err := parseIssueNumber(form.Query)
	if err != nil {
		p.Error = fmt.Errorf("invalid form value %q: %w", form.Query, err).Error()
		return p
	}
	if proj == "" {
		proj = g.githubProject // default to golang/go
	}
	if g.githubProject != proj {
		p.Error = fmt.Errorf("invalid form value (unrecognized project): %q", form.Query).Error()
		return p
	}
	if issue <= 0 {
		return p
	}
	// Find issue in database.
	i, err := github.LookupIssue(g.db, proj, issue)
	if err != nil {
		return rulesPage{
			rulesForm: form,
			Error:     fmt.Errorf("error looking up issue %q: %w", form.Query, err).Error(),
		}
	}

	// TODO: this llm.TextGenerator cast is kind of ugly. Redo somehow.
	rules, err := rules.Issue(r.Context(), g.embed.(llm.TextGenerator), i)
	if err != nil {
		return rulesPage{
			rulesForm: form,
			Error:     err.Error(),
		}
	}
	return rulesPage{
		rulesForm: form,
		Result: &rulesResult{
			Issue:       i,
			IssueResult: *rules,
			HTML:        htmlutil.MarkdownToSafeHTML(rules.Response),
		},
	}
}

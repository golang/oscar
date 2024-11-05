// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/safehtml"
	"github.com/google/safehtml/template"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/htmlutil"
	"golang.org/x/oscar/internal/llm"
)

// rulesPage holds the fields needed to display the results
// of an issue rule check.
type rulesPage struct {
	rulesForm // the raw form inputs
	Result    *rulesResult
	Error     string // if non-empty, the error to display instead of the result
}

type rulesResult struct {
	github.IssueRulesResult               // the raw result
	HTML                    safehtml.HTML // issue response as HTML
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
	if form.Query == "" {
		return rulesPage{
			rulesForm: form,
		}
	}
	issue, err := strconv.Atoi(strings.TrimSpace(form.Query))
	if err != nil {
		return rulesPage{
			rulesForm: form,
			Error:     fmt.Errorf("invalid form value %q: %w", form.Query, err).Error(),
		}
	}
	// TODO: this llm.TextGenerator cast is kind of ugly. Redo somehow.
	rules, err := github.IssueRules(r.Context(), g.embed.(llm.TextGenerator), g.db, g.githubProject, int64(issue))
	if err != nil {
		return rulesPage{
			rulesForm: form,
			Error:     err.Error(),
		}
	}
	return rulesPage{
		rulesForm: form,
		Result: &rulesResult{
			IssueRulesResult: *rules,
			HTML:             htmlutil.MarkdownToSafeHTML(rules.Response),
		},
	}
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/google/safehtml/template"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/oscar/internal/actions"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/llmapp"
	"golang.org/x/oscar/internal/search"
)

func TestTemplates(t *testing.T) {
	for _, test := range []struct {
		name  string
		tmpl  *template.Template
		value testPage
	}{
		{"search", searchPageTmpl, &searchPage{Results: []search.Result{{Kind: "k", Title: "t"}}}},
		{"actionlog", actionLogPageTmpl, &actionLogPage{
			StartTime: "t",
			Entries:   []*actions.Entry{{Kind: "k"}},
		}},
		{"overview-initial", overviewPageTmpl, &overviewPage{}},
		{"overview", overviewPageTmpl, &overviewPage{
			Params: overviewParams{Query: "12"},
			Result: &overviewResult{
				Raw: &llmapp.Result{
					Response: "an overview",
					Cached:   true,
					Prompt:   []llm.Part{llm.Text("a prompt")},
				},
				Typed: github.IssueOverviewResult{
					TotalComments: 2,
					Overview: &llmapp.Result{
						Response: "an overview",
						Cached:   true,
						Prompt:   []llm.Part{llm.Text("a prompt")},
					},
				},
				Issue: &github.Issue{
					User:      github.User{Login: "abc"},
					CreatedAt: "2023-01-01T0",
					HTMLURL:   "https://example.com",
				},
				Type: issueOverviewType,
			}}},
		{"overview-error", overviewPageTmpl, &overviewPage{
			Params: overviewParams{Query: "12"},
			Error:  fmt.Errorf("an error"),
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.value.setCommonPage()
			b, err := Exec(test.tmpl, test.value)
			if err != nil {
				t.Fatal(err)
			}
			html := string(b)
			if err := validateHTML(html); err != nil {
				printNumbered(html)
				t.Fatalf("\n%s", err)
			}
		})
	}
}

type testPage interface {
	setCommonPage()
	page
}

func printNumbered(s string) {
	for i, line := range strings.Split(s, "\n") {
		fmt.Printf("%3d %s\n", i+1, line)
	}
}

// validateHTML performs basic HTML validation.
// It checks that every start tag has a matching end tag.
func validateHTML(s string) error {
	type tag struct {
		line int
		a    atom.Atom
	}

	var errs []error
	var stack []tag

	r := newLineReader(strings.NewReader(s))
	tizer := html.NewTokenizer(r)
	for tizer.Err() == nil {
		tt := tizer.Next()
		switch tt {
		case html.ErrorToken:
			if tizer.Err() != io.EOF {
				errs = append(errs, tizer.Err())
			}
		case html.StartTagToken:
			stack = append(stack, tag{r.line, tizer.Token().DataAtom})
		case html.EndTagToken:
			end := tizer.Token().DataAtom
			n := len(stack)
			if n == 0 {
				errs = append(errs, fmt.Errorf("no start tag matching end tag </%s> on line %d", end, r.line))
			} else {
				top := stack[n-1]
				if top.a != end {
					errs = append(errs, fmt.Errorf("end tag </%s> on line %d does not match start tag <%s> on line %d",
						end, r.line, top.a, top.line))
					// don't pop the stack
				} else {
					stack = stack[:n-1]
				}
			}
		default:
			// ignore
		}
	}
	return errors.Join(errs...)
}

// A lineReader is an io.Reader that tracks line numbers.
type lineReader struct {
	line int
	rest []byte
	r    io.Reader
}

func newLineReader(r io.Reader) *lineReader {
	return &lineReader{line: 1, r: r}
}

func (r *lineReader) Read(buf []byte) (n int, err error) {
	if len(r.rest) == 0 {
		n, err = r.r.Read(buf)
		r.rest = slices.Clone(buf[:n])
	}
	i := bytes.IndexByte(r.rest, '\n')
	if i < 0 {
		i = len(r.rest) - 1
	} else {
		r.line++
	}
	n = copy(buf, r.rest[:i+1])
	r.rest = r.rest[i+1:]
	return n, err
}

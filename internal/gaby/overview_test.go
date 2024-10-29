// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/embeddocs"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/llmapp"
)

func TestPopulateOverviewPage(t *testing.T) {
	g := newTestGaby(t)

	// Add test data relevant to this test.
	project := "hello/world"
	g.githubProject = project
	g.github.Add(project)

	iss1 := &github.Issue{
		URL:     "https://api.github.com/repos/hello/world/issues/1",
		HTMLURL: "https://github.com/hello/world/issues/1",
		Number:  1,
		Title:   "hello",
		Body:    "hello world",
	}
	iss2 := &github.Issue{
		URL:     "https://api.github.com/repos/hello/world/issues/2",
		HTMLURL: "https://github.com/hello/world/issues/2",
		Number:  2,
		Title:   "hello 2",
		Body:    "hello world 2",
	}
	g.github.Testing().AddIssue(project, iss1)
	comment := &github.IssueComment{
		Body: "a comment",
	}
	g.github.Testing().AddIssueComment(project, 1, comment)
	g.github.Testing().AddIssue(project, iss2)

	ctx := context.Background()
	docs.Sync(g.docs, g.github)
	embeddocs.Sync(ctx, g.slog, g.vector, g.embed, g.docs)

	// Generate expected overviews.
	wantIssueOverview, err := g.llm.PostOverview(ctx, &llmapp.Doc{
		Type:  "issue",
		URL:   iss1.HTMLURL,
		Title: iss1.Title,
		Text:  iss1.Body,
	}, []*llmapp.Doc{
		{
			Type: "issue comment",
			URL:  comment.HTMLURL,
			Text: comment.Body,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantRelatedOverview, err := g.llm.RelatedOverview(ctx, &llmapp.Doc{
		Type:  "main",
		URL:   iss1.HTMLURL,
		Title: iss1.Title,
		Text:  iss1.Body,
	}, []*llmapp.Doc{
		{
			Type:  "related",
			URL:   iss2.HTMLURL,
			Title: iss2.Title,
			Text:  iss2.Body,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		r    *http.Request
		want overviewPage
	}{
		{
			name: "empty",
			r:    &http.Request{},
			want: overviewPage{},
		},
		{
			name: "issue overview (default)",
			r: &http.Request{
				Form: map[string][]string{
					"q": {"1"},
				},
			},
			want: overviewPage{
				Form: overviewForm{
					Query:        "1",
					OverviewType: "",
				},
				Result: &overviewResult{
					IssueOverviewResult: github.IssueOverviewResult{
						Issue:       iss1,
						NumComments: 1,
						Overview:    wantIssueOverview,
					},
					Type: issueOverviewType,
				},
			},
		},
		{
			name: "issue overview (explicit)",
			r: &http.Request{
				Form: map[string][]string{
					"q": {"1"},
					"t": {issueOverviewType},
				},
			},
			want: overviewPage{
				Form: overviewForm{
					Query:        "1",
					OverviewType: issueOverviewType,
				},
				Result: &overviewResult{
					IssueOverviewResult: github.IssueOverviewResult{
						Issue:       iss1,
						NumComments: 1,
						Overview:    wantIssueOverview,
					},
					Type: issueOverviewType,
				},
			},
		},
		{
			name: "related overview",
			r: &http.Request{
				Form: map[string][]string{
					"q": {"1"},
					"t": {relatedOverviewType},
				},
			},
			want: overviewPage{
				Form: overviewForm{
					Query:        "1",
					OverviewType: relatedOverviewType,
				},
				Result: &overviewResult{
					IssueOverviewResult: github.IssueOverviewResult{
						Issue:    iss1,
						Overview: wantRelatedOverview,
					},
					Type: relatedOverviewType,
				},
			},
		},
		{
			name: "error",
			r: &http.Request{
				Form: map[string][]string{
					"q": {"3"}, // not in DB
					"t": {relatedOverviewType},
				},
			},
			want: overviewPage{
				Form: overviewForm{
					Query:        "3",
					OverviewType: relatedOverviewType,
				},
				Error: cmpopts.AnyError,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := g.populateOverviewPage(tc.r)
			if diff := cmp.Diff(got, tc.want,
				cmpopts.IgnoreFields(llmapp.OverviewResult{}, "Cached"),
				cmpopts.EquateErrors()); diff != "" {
				t.Errorf("Gaby.populateOverviewPage() mismatch (-got +want):\n%s", diff)
			}
		})
	}

}

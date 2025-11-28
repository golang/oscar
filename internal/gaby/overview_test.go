// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/safehtml"
	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/embeddocs"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/llmapp"
	"golang.org/x/oscar/internal/overview"
	"golang.org/x/oscar/internal/search"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestPopulateOverviewPage(t *testing.T) {
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	gh := github.New(lg, db, secret.Empty(), nil)
	lc := llmapp.New(lg, llmapp.RelatedTestGenerator(t, 1), db)
	g := &Gaby{
		slog:     lg,
		db:       db,
		vector:   storage.MemVectorDB(db, lg, "vector"),
		github:   github.New(lg, db, secret.Empty(), nil),
		llmapp:   lc,
		overview: overview.New(lg, db, gh, lc, "test", "test-bot"),
		docs:     docs.New(lg, db),
		embed:    llm.QuoteEmbedder(),
	}

	// Add test data relevant to this test.
	project := "hello/world"
	g.githubProjects = []string{project}
	g.github.Add(project)

	iss1 := &github.Issue{
		Number: 1,
		Title:  "hello",
		Body:   "hello world",
	}
	iss2 := &github.Issue{
		Number: 2,
		Title:  "hello 2",
		Body:   "hello world 2",
	}
	comment := &github.IssueComment{
		Body: "a comment",
	}
	comment2 := &github.IssueComment{
		Body: "another comment",
	}
	// Note: these calls populate the ID, HTMLURL and URL fields
	// of the issues and comments.
	g.github.Testing().AddIssue(project, iss1)
	g.github.Testing().AddIssueComment(project, 1, comment)
	g.github.Testing().AddIssueComment(project, 1, comment2)
	g.github.Testing().AddIssue(project, iss2)

	commentID := strconv.Itoa(int(comment.CommentID()))

	ctx := context.Background()
	docs.Sync(g.docs, g.github)
	embeddocs.Sync(ctx, g.slog, g.vector, g.embed, g.docs)

	// Generate expected overviews.
	// This only tests that the correct calls are made; the internals
	// of these functions are tested in their respective packages.
	wantIssueResult, err := g.overview.ForIssue(ctx, iss1)
	if err != nil {
		t.Fatal(err)
	}
	wantUpdateResult, err := g.overview.ForIssueUpdate(ctx, iss1, comment.CommentID())
	if err != nil {
		t.Fatal(err)
	}
	wantRelatedResult, err := search.Analyze(ctx, g.llmapp, g.vector, g.docs, iss1.HTMLURL)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		r    *http.Request
		want *overviewPage
	}{
		{
			name: "empty",
			r:    &http.Request{},
			want: &overviewPage{},
		},
		{
			name: "issue overview (default)",
			r: &http.Request{
				Form: map[string][]string{
					"q": {"1"},
				},
			},
			want: &overviewPage{
				Params: overviewParams{
					Query:        "1",
					OverviewType: "",
				},
				Result: &overviewResult{
					Raw: wantIssueResult.Overview,
					Typed: &overview.IssueResult{
						TotalComments: 2,
						LastComment:   comment2.CommentID(),
						Overview:      wantIssueResult.Overview,
					},
					Issue: iss1,
					Type:  issueOverviewType,
					Desc:  "issue 1 and all 2 comments",
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
			want: &overviewPage{
				Params: overviewParams{
					Query:        "1",
					OverviewType: issueOverviewType,
				},
				Result: &overviewResult{
					Raw: wantIssueResult.Overview,
					Typed: &overview.IssueResult{
						TotalComments: 2,
						LastComment:   comment2.CommentID(),
						Overview:      wantIssueResult.Overview,
					},
					Issue: iss1,
					Type:  issueOverviewType,
					Desc:  "issue 1 and all 2 comments",
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
			want: &overviewPage{
				Params: overviewParams{
					Query:        "1",
					OverviewType: relatedOverviewType,
				},
				Result: &overviewResult{
					Raw: &wantRelatedResult.Result,
					Typed: &search.Analysis{
						RelatedAnalysis: wantRelatedResult.RelatedAnalysis,
					},
					Issue: iss1,
					Type:  relatedOverviewType,
					Desc:  "issue 1 and 1 related docs",
				},
			},
		},
		{
			name: "update overview",
			r: &http.Request{
				Form: map[string][]string{
					paramQuery:        {"1"},
					paramOverviewType: {updateOverviewType},
					paramLastRead:     {commentID},
				},
			},
			want: &overviewPage{
				Params: overviewParams{
					Query:           "1",
					OverviewType:    updateOverviewType,
					LastReadComment: commentID,
				},
				Result: &overviewResult{
					Raw: wantUpdateResult.Overview,
					Typed: &overview.IssueUpdateResult{
						NewComments:   1,
						TotalComments: 2,
						LastComment:   comment2.CommentID(),
						Overview:      wantUpdateResult.Overview,
					},
					Issue: iss1,
					Type:  updateOverviewType,
					Desc:  fmt.Sprintf("issue 1 and its 1 new comments after %d", comment.CommentID()),
				},
			},
		},
		{
			name: "error/unknownIssue",
			r: &http.Request{
				Form: map[string][]string{
					"q": {"3"}, // not in DB
					"t": {relatedOverviewType},
				},
			},
			want: &overviewPage{
				Params: overviewParams{
					Query:        "3",
					OverviewType: relatedOverviewType,
				},
				Error: cmpopts.AnyError,
			},
		},
		{
			name: "error/unknownProject",
			r: &http.Request{
				Form: map[string][]string{
					"q": {"unknown/project#3"}, // not in DB
					"t": {relatedOverviewType},
				},
			},
			want: &overviewPage{
				Params: overviewParams{
					Query:        "unknown/project#3",
					OverviewType: relatedOverviewType,
				},
				Error: cmpopts.AnyError,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := g.populateOverviewPage(tc.r)
			tc.want.setCommonPage()
			if diff := cmp.Diff(got, tc.want,
				cmpopts.IgnoreFields(llmapp.Result{}, "Cached"),
				cmpopts.EquateErrors(),
				safeHTMLcmpopt); diff != "" {
				t.Errorf("Gaby.populateOverviewPage() mismatch (-got +want):\n%s", diff)
			}
		})
	}

}

var safeHTMLcmpopt = cmpopts.EquateComparable(safehtml.TrustedResourceURL{}, safehtml.Identifier{})

func TestParseOverviewPageQuery(t *testing.T) {
	tests := []struct {
		in          string
		wantProject string
		wantIssue   int64
		wantErr     bool
	}{
		{
			in: "",
		},
		{
			in:        "12345",
			wantIssue: 12345,
		},
		{
			in:          "golang/go#12345",
			wantProject: "golang/go",
			wantIssue:   12345,
		},
		{
			in:        " 123",
			wantIssue: 123,
		},
		{
			in:      "x012x",
			wantErr: true,
		},
		{
			in:      "golang/go",
			wantErr: true,
		},
		{
			in:          "https://github.com/foo/bar/issues/12345",
			wantProject: "foo/bar",
			wantIssue:   12345,
		},
		{
			in:          "https://go.dev/issues/234",
			wantProject: "golang/go",
			wantIssue:   234,
		},
		{
			in:          "github.com/foo/bar/issues/12345",
			wantProject: "foo/bar",
			wantIssue:   12345,
		},
		{
			in:          "go.dev/issues/234",
			wantProject: "golang/go",
			wantIssue:   234,
		},
		{
			in:      "https://example.com/foo/bar/issues/12345",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			proj, issue, err := parseIssueNumber(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseOverviewPageQuery(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if proj != tt.wantProject || issue != tt.wantIssue {
				t.Errorf("parseOverviewPageQuery(%q) = (%q, %d, %v), want (%q, %d, _)", tt.in, proj, issue, err, tt.wantProject, tt.wantIssue)
			}
		})
	}
}

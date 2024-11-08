// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/llmapp"
	"golang.org/x/oscar/internal/search"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestSearchPageTemplate(t *testing.T) {
	for _, tc := range []struct {
		name string
		page searchPage
	}{
		{
			name: "results",
			page: searchPage{
				Params: searchParams{
					Query: "some query",
				},
				Results: []search.Result{
					{
						Kind:  "Example",
						Title: "t1",
						VectorResult: storage.VectorResult{
							ID:    "https://example.com/x",
							Score: 0.987654321,
						},
					},
					{
						Kind: "",
						VectorResult: storage.VectorResult{
							ID:    "https://example.com/y",
							Score: 0.876,
						},
					},
				},
			},
		},
		{
			name: "error",
			page: searchPage{
				Params: searchParams{
					Query: "some query",
				},
				Error: errors.New("some error"),
			},
		},
		{
			name: "no results",
			page: searchPage{
				Params: searchParams{
					Query: "some query",
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.page.setCommonPage()
			b, err := Exec(searchPageTmpl, &tc.page)
			if err != nil {
				t.Fatal(err)
			}
			got := string(b)

			if len(tc.page.Results) != 0 {
				wants := []string{tc.page.Params.Query}
				for _, sr := range tc.page.Results {
					wants = append(wants, sr.VectorResult.ID)
				}
				t.Logf("%s", got)
				for _, w := range wants {
					if !strings.Contains(got, w) {
						t.Errorf("did not find %q in HTML", w)
					}
				}
			} else if e := tc.page.Error; e != nil {
				if !strings.Contains(got, e.Error()) {
					t.Errorf("did not find error %q in HTML", e)
				}
			} else {
				want := "No results"
				if !strings.Contains(got, want) {
					t.Errorf("did not find %q in HTML", want)
				}
			}
		})
	}
}

func TestToOptions(t *testing.T) {
	tests := []struct {
		name    string
		form    searchParams
		want    *search.Options
		wantErr bool
	}{
		{
			name: "basic",
			form: searchParams{
				Threshold: ".55",
				Limit:     "10",
				Allow:     "GoBlog,GoDevPage,GitHubIssue",
				Deny:      "GoDevPage,GoWiki",
			},
			want: &search.Options{
				Threshold: .55,
				Limit:     10,
				AllowKind: []string{search.KindGoBlog, search.KindGoDevPage, search.KindGitHubIssue},
				DenyKind:  []string{search.KindGoDevPage, search.KindGoWiki},
			},
		},
		{
			name: "empty",
			form: searchParams{},
			// this will cause search to use defaults
			want: &search.Options{},
		},
		{
			name: "trim spaces",
			form: searchParams{
				Threshold: " .55	 ",
				Limit:     "  10 ",
				Allow:     " GoBlog,  GoDevPage,GitHubIssue ",
				Deny:      "	GoDevPage, GoWiki		",
			},
			want: &search.Options{
				Threshold: .55,
				Limit:     10,
				AllowKind: []string{search.KindGoBlog, search.KindGoDevPage, search.KindGitHubIssue},
				DenyKind:  []string{search.KindGoDevPage, search.KindGoWiki},
			},
		},
		{
			name: "unparseable limit",
			form: searchParams{
				Limit: "1.xx",
			},
			wantErr: true,
		},
		{
			name: "invalid limit",
			form: searchParams{
				Limit: "1.33",
			},
			wantErr: true,
		},
		{
			name: "unparseable threshold",
			form: searchParams{
				Threshold: "1x",
			},
			wantErr: true,
		},
		{
			name: "invalid threshold",
			form: searchParams{
				Threshold: "-10",
			},
			wantErr: true,
		},
		{
			name: "invalid allow",
			form: searchParams{
				Allow: "NotAKind, also not a kind",
			},
			wantErr: true,
		},
		{
			name: "invalid deny",
			form: searchParams{
				Deny: "NotAKind, also not a kind",
			},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.form.toOptions()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Params.toOptions() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Params.toOptions() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPopulateSearchPage(t *testing.T) {
	g := newTestGaby(t)

	// Add test data relevant for this test.
	g.docs.Add("id1", "hello", "hello world")
	g.embedAll(context.Background())

	for _, tc := range []struct {
		name string
		url  string
		want *searchPage
	}{
		{
			name: "query",
			url:  "test/search?q=hello",
			want: &searchPage{
				Params: searchParams{
					Query: "hello",
				},
				Results: []search.Result{
					{
						Kind:  search.KindUnknown,
						Title: "hello",
						VectorResult: storage.VectorResult{
							ID:    "id1",
							Score: 0.526,
						},
					},
				}},
		},
		{
			name: "id lookup",
			url:  "test/search?q=id1",
			want: &searchPage{
				Params: searchParams{
					Query: "id1",
				},
				Results: []search.Result{{
					Kind:  search.KindUnknown,
					Title: "hello",
					VectorResult: storage.VectorResult{
						ID:    "id1",
						Score: 1, // exact same
					},
				}}},
		},
		{
			name: "options",
			url:  "test/search?q=id1&threshold=.5&limit=10&allow_kind=&deny_kind=Unknown,GoBlog",
			want: &searchPage{
				Params: searchParams{
					Query:     "id1",
					Threshold: ".5",
					Limit:     "10",
					Allow:     "",
					Deny:      "Unknown,GoBlog",
				},
				// No results (blocked by DenyKind)
			},
		},
		{
			name: "error",
			url:  "test/search?q=id1&deny_kind=Invalid",
			want: &searchPage{
				Params: searchParams{
					Query: "id1",
					Deny:  "Invalid",
				},
				Error: cmpopts.AnyError,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := http.NewRequest(http.MethodGet, tc.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := g.populateSearchPage(r)
			tc.want.setCommonPage()
			if diff := cmp.Diff(tc.want, got, safeHTMLcmpopt, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("Gaby.populateSearchPage mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func newTestGaby(t *testing.T) *Gaby {
	t.Helper()

	lg := testutil.Slogger(t)
	db := storage.MemDB()

	g := &Gaby{
		slog:   lg,
		db:     db,
		vector: storage.MemVectorDB(db, lg, "vector"),
		github: github.New(lg, db, secret.Empty(), nil),
		llm:    llmapp.New(lg, llm.EchoTextGenerator(), db),
		docs:   docs.New(lg, db),
		embed:  llm.QuoteEmbedder(),
	}

	return g
}

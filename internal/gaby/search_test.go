// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/embeddocs"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/search"
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
				searchForm: searchForm{
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
				searchForm: searchForm{
					Query: "some query",
				},
				SearchError: "some error",
			},
		},
		{
			name: "no results",
			page: searchPage{
				searchForm: searchForm{
					Query: "some query",
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := searchPageTmpl.Execute(&buf, tc.page); err != nil {
				t.Fatal(err)
			}
			got := buf.String()

			if len(tc.page.Results) != 0 {
				wants := []string{tc.page.Query}
				for _, sr := range tc.page.Results {
					wants = append(wants, sr.VectorResult.ID)
				}
				t.Logf("%s", got)
				for _, w := range wants {
					if !strings.Contains(got, w) {
						t.Errorf("did not find %q in HTML", w)
					}
				}
			} else if e := tc.page.SearchError; e != "" {
				if !strings.Contains(got, e) {
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
		form    searchForm
		want    *search.Options
		wantErr bool
	}{
		{
			name: "basic",
			form: searchForm{
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
			form: searchForm{},
			// this will cause search to use defaults
			want: &search.Options{},
		},
		{
			name: "trim spaces",
			form: searchForm{
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
			form: searchForm{
				Limit: "1.xx",
			},
			wantErr: true,
		},
		{
			name: "invalid limit",
			form: searchForm{
				Limit: "1.33",
			},
			wantErr: true,
		},
		{
			name: "unparseable threshold",
			form: searchForm{
				Threshold: "1x",
			},
			wantErr: true,
		},
		{
			name: "invalid threshold",
			form: searchForm{
				Threshold: "-10",
			},
			wantErr: true,
		},
		{
			name: "invalid allow",
			form: searchForm{
				Allow: "NotAKind, also not a kind",
			},
			wantErr: true,
		},
		{
			name: "invalid deny",
			form: searchForm{
				Deny: "NotAKind, also not a kind",
			},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.form.toOptions()
			if (err != nil) != tc.wantErr {
				t.Fatalf("searchForm.toOptions() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("searchForm.toOptions() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPopulatePage(t *testing.T) {
	ctx := context.Background()
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	dc := docs.New(lg, db)
	vector := storage.MemVectorDB(db, lg, "vector")
	dc.Add("id1", "hello", "hello world")
	embedder := llm.QuoteEmbedder()
	embeddocs.Sync(ctx, lg, vector, embedder, dc)
	g := &Gaby{
		slog:   lg,
		db:     db,
		vector: vector,
		docs:   dc,
		embed:  embedder,
	}

	for _, tc := range []struct {
		name string
		url  string
		want searchPage
	}{
		{
			name: "query",
			url:  "test/search?q=hello",
			want: searchPage{
				searchForm: searchForm{
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
			want: searchPage{
				searchForm: searchForm{
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
			want: searchPage{
				searchForm: searchForm{
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
			want: searchPage{
				searchForm: searchForm{
					Query: "id1",
					Deny:  "Invalid",
				},
				SearchError: `invalid form value: unrecognized deny kind "Invalid" (case-sensitive)`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := http.NewRequest(http.MethodGet, tc.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := g.populatePage(r)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Gaby.search() = %v, want %v", got, tc.want)
			}
		})
	}
}

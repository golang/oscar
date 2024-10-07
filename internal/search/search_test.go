// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package search

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestKind(t *testing.T) {
	for _, test := range []struct {
		id, want string
	}{
		{"something", "Unknown"},
		{"https://go.dev/x", "GoDevPage"},
		{"https://go.dev/blog/xxx", "GoBlog"},
		{"https://go.dev/doc/x", "GoDocumentation"},
		{"https://go.dev/ref/x", "GoReference"},
		{"https://go.dev/wiki/x", "GoWiki"},
		{"https://github.com/golang/go/issues/123", "GitHubIssue"},
		{"https://go-review.googlesource.com/c/test/+/1#related-content", "GoGerritChange"},
	} {
		got := docIDKind(test.id)
		if got != test.want {
			t.Errorf("%q: got %q, want %q", test.id, got, test.want)
		}
	}
}

func TestSearch(t *testing.T) {
	ctx := context.Background()
	lg := testutil.Slogger(t)
	embedder := llm.QuoteEmbedder()
	db := storage.MemDB()
	vdb := storage.MemVectorDB(db, lg, "")
	corpus := docs.New(lg, db)

	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("id%d", i)
		doc := llm.EmbedDoc{Title: fmt.Sprintf("title%d", i), Text: fmt.Sprintf("text-%s", strings.Repeat("x", i))}
		corpus.Add(id, doc.Title, doc.Text)
		vec := mustEmbed(t, embedder, doc)
		vdb.Set(id, vec)
	}

	opts := Options{
		Threshold: 0,
		Limit:     2,
	}
	doc := llm.EmbedDoc{Title: "title3", Text: "text-xxx"}
	qreq := &QueryRequest{
		Options:  opts,
		EmbedDoc: doc,
	}
	gotQ, err := Query(ctx, vdb, corpus, embedder, qreq)
	if err != nil {
		t.Fatal(err)
	}
	round(gotQ)

	vreq := &VectorRequest{
		Options: opts,
		Vector:  mustEmbed(t, embedder, doc),
	}
	gotV := Vector(vdb, corpus, vreq)
	round(gotV)

	want := []Result{
		{
			Kind:         KindUnknown,
			Title:        "title3",
			VectorResult: storage.VectorResult{ID: "id3", Score: 1.0},
		},
		{
			Kind:         KindUnknown,
			Title:        "title4",
			VectorResult: storage.VectorResult{ID: "id4", Score: 0.56},
		},
	}

	if !slices.Equal(gotQ, want) {
		t.Errorf("Query: got  %v\nwant %v", gotQ, want)
	}

	if !slices.Equal(gotV, want) {
		t.Errorf("Vector: got  %v\nwant %v", gotQ, want)
	}

	qreq.Threshold = 0.9
	gotQ, err = Query(ctx, vdb, corpus, embedder, qreq)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotQ) != 1 {
		t.Errorf("got %d results, want 1", len(gotQ))
	}

	vreq.Threshold = 0.9
	gotV = Vector(vdb, corpus, vreq)
	if len(gotV) != 1 {
		t.Errorf("got %d results, want 1", len(gotQ))
	}
}

func round(rs []Result) {
	for i := range rs {
		rs[i].Round()
	}
}

func TestOptions(t *testing.T) {
	ctx := context.Background()
	lg := testutil.Slogger(t)
	embedder := llm.QuoteEmbedder()
	db := storage.MemDB()
	vdb := storage.MemVectorDB(db, lg, "")
	corpus := docs.New(lg, db)

	ids := []string{
		0:  "https://go.dev/blog/topic",
		1:  "https://github.com/golang/go/issues/11",
		2:  "not-a-url",
		3:  "https://go.dev/doc/something",
		4:  "https://go.dev/ref/something",
		5:  "https://go.dev/page",
		6:  "https://go.dev/blog/another/topic",
		7:  "https://github.com/golang/go/issues/42",
		8:  "https://go.dev/wiki/something",
		9:  "https://pkg.go.dev/",
		10: "https://go-review.googlesource.com/c/go/+/1",
	}

	for i, id := range ids {
		doc := llm.EmbedDoc{
			Title: fmt.Sprintf("title%d", i),
			Text:  fmt.Sprintf("text-%s", strings.Repeat("x", i))}
		corpus.Add(id, doc.Title, doc.Text)
		vec := mustEmbed(t, embedder, doc)
		vdb.Set(id, vec)
	}

	doc := llm.EmbedDoc{Title: "title3", Text: "text-xxx"}
	results := []Result{
		0: {
			Kind:         KindGoDocumentation,
			Title:        "title3",
			VectorResult: storage.VectorResult{ID: ids[3], Score: 1.0},
		},
		1: {
			Kind:         KindGoReference,
			Title:        "title4",
			VectorResult: storage.VectorResult{ID: ids[4], Score: 0.56},
		},
		2: {
			Kind:         KindGoDevPage,
			Title:        "title5",
			VectorResult: storage.VectorResult{ID: ids[5], Score: 0.544},
		},
		3: {
			Kind:         KindUnknown,
			Title:        "title2",
			VectorResult: storage.VectorResult{ID: ids[2], Score: 0.531},
		},
		4: {
			Kind:         KindGoBlog,
			Title:        "title6",
			VectorResult: storage.VectorResult{ID: ids[6], Score: 0.529},
		},
		5: {
			Kind:         KindGitHubIssue,
			Title:        "title7",
			VectorResult: storage.VectorResult{ID: ids[7], Score: 0.516},
		},
		6: {
			Kind:         KindGoWiki,
			Title:        "title8",
			VectorResult: storage.VectorResult{ID: ids[8], Score: 0.503},
		},
		7: {
			Kind:         KindUnknown,
			Title:        "title9",
			VectorResult: storage.VectorResult{ID: ids[9], Score: 0.492},
		},
		8: {
			Kind:         KindGitHubIssue,
			Title:        "title1",
			VectorResult: storage.VectorResult{ID: ids[1], Score: 0.483},
		},
		9: {
			Kind:         KindGoGerritChange,
			Title:        "title10",
			VectorResult: storage.VectorResult{ID: ids[10], Score: 0.481},
		},
		10: {
			Kind:         KindGoBlog,
			Title:        "title0",
			VectorResult: storage.VectorResult{ID: ids[0], Score: 0.431},
		},
	}

	for _, tc := range []struct {
		name    string
		options Options
		want    []Result
	}{
		{
			name: "no options",
			want: results,
		},
		{
			name: "threshold",
			options: Options{
				Threshold: .5,
			},
			want: results[:7],
		},
		{
			name: "limit",
			options: Options{
				Limit: 5,
			},
			want: results[:5],
		},
		{
			// Limit wins.
			name: "limit-threshold",
			options: Options{
				Threshold: .5,
				Limit:     5,
			},
			want: results[:5],
		},
		{
			// Threshold wins.
			name: "threshold-limit",
			options: Options{
				Threshold: .5,
				Limit:     10,
			},
			want: results[:7],
		},
		{
			name: "allow",
			options: Options{
				AllowKind: []string{KindGoWiki, KindGitHubIssue},
			},
			want: []Result{
				results[5], // issue
				results[6], // wiki
				results[8], // issue
			},
		},
		{
			name: "allow-limit",
			options: Options{
				AllowKind: []string{KindGoWiki, KindGitHubIssue},
				Limit:     6,
			},
			want: []Result{results[5]},
		},
		{
			name: "allow-threshold",
			options: Options{
				AllowKind: []string{KindGoWiki, KindGitHubIssue},
				Threshold: .5,
			},
			want: []Result{results[5], results[6]},
		},
		{
			name: "deny",
			options: Options{
				DenyKind: []string{KindGoWiki, KindGitHubIssue, KindGoGerritChange},
			},
			want: []Result{
				results[0], results[1], results[2], results[3], results[4],
				// skip 5 (issue) and 6 (wiki)
				results[7],
				// skip 8 (issue) and 9 (change)
				results[10]},
		},
		{
			name: "allow-deny",
			options: Options{
				AllowKind: []string{KindGoWiki, KindGitHubIssue},
				DenyKind:  []string{KindGitHubIssue},
			},
			// Only wikis are allowed.
			want: []Result{results[6]},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Query(ctx, vdb, corpus, embedder,
				&QueryRequest{
					Options:  tc.options,
					EmbedDoc: doc,
				})
			if err != nil {
				t.Fatal(err)
			}
			round(got)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Query() mismatch (-want +got):\n%s", diff)
			}
		})
	}

}

func mustEmbed(t *testing.T, embedder llm.Embedder, doc llm.EmbedDoc) llm.Vector {
	t.Helper()
	vec, err := embedder.EmbedDocs(context.Background(), []llm.EmbedDoc{doc})
	if err != nil {
		t.Fatal(err)
	}
	return vec[0]
}

func TestSearchJSON(t *testing.T) {
	// Confirm that we can unmarshal a search request, and marshal a response.
	postBody := `{"Limit": 10, "Threshold": 0.8, "AllowKind": ["GoWiki"], "DenyKind": [""], "Title": "t", "Text": "text"}`
	var gotReq QueryRequest
	if err := json.Unmarshal([]byte(postBody), &gotReq); err != nil {
		t.Fatal(err)
	}
	wantReq := QueryRequest{
		Options: Options{
			Limit:     10,
			Threshold: 0.8,
			AllowKind: []string{"GoWiki"},
			DenyKind:  []string{""},
		},
		EmbedDoc: llm.EmbedDoc{Title: "t", Text: "text"}}
	if !reflect.DeepEqual(gotReq, wantReq) {
		t.Errorf("got %+v, want %+v", gotReq, wantReq)
	}

	res := []Result{
		{Kind: "K", Title: "t", VectorResult: storage.VectorResult{ID: "id", Score: .5}},
	}
	bytes, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	got := string(bytes)
	want := `[{"Kind":"K","Title":"t","ID":"id","Score":0.5}]`
	if got != want {
		t.Errorf("\ngot  %s\nwant %s", got, want)
	}
}

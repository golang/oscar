// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package search

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestKind(t *testing.T) {
	for _, test := range []struct {
		id, want string
	}{
		{"something", ""},
		{"https://go.dev/x", "GoDevPage"},
		{"https://go.dev/blog/xxx", "GoBlog"},
		{"https://go.dev/doc/x", "GoDocumentation"},
		{"https://go.dev/ref/x", "GoReference"},
		{"https://go.dev/wiki/x", "GoWiki"},
		{"https://github.com/golang/go/issues/123", "GitHubIssue"},
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
	for i := range gotQ {
		gotQ[i].Round()
	}

	vreq := &VectorRequest{
		Options: opts,
		Vector:  mustEmbed(t, embedder, doc),
	}
	gotV := Vector(vdb, corpus, vreq)
	for i := range gotV {
		gotV[i].Round()
	}

	want := []Result{
		{
			Kind:         "",
			Title:        "title3",
			VectorResult: storage.VectorResult{ID: "id3", Score: 1.0},
		},
		{
			Kind:         "",
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
	postBody := `{"Limit": 10, "Threshold": 0.8, "Title": "t", "Text": "text"}`
	var gotReq QueryRequest
	if err := json.Unmarshal([]byte(postBody), &gotReq); err != nil {
		t.Fatal(err)
	}
	wantReq := QueryRequest{Options: Options{Limit: 10, Threshold: 0.8}, EmbedDoc: llm.EmbedDoc{Title: "t", Text: "text"}}
	if gotReq != wantReq {
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

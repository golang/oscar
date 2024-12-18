// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package search

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/embeddocs"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/llmapp"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestAnalyze(t *testing.T) {
	ctx := context.Background()
	lg := testutil.Slogger(t)

	g := llmapp.RelatedTestGenerator(t, 1)
	db := storage.MemDB()
	lc := llmapp.New(lg, g, db)
	vdb := storage.MemVectorDB(db, lg, "test")
	dc := docs.New(lg, db)

	mr := maxResults
	maxResults = 1
	t.Cleanup(func() {
		maxResults = mr
	})

	id := "https://example.com/123"
	dc.Add(id, "title", "text")
	dc.Add("456", "title2", "text2")
	dc.Add("3", "title3", "text3")

	// Add the documents to vdb.
	testutil.Check(t, embeddocs.Sync(ctx, lg, vdb, llm.QuoteEmbedder(), dc))

	got, err := Analyze(ctx, lc, vdb, dc, id)
	if err != nil {
		t.Fatal(err)
	}

	doc1 := &llmapp.Doc{
		Type:  "main",
		URL:   id,
		Title: "title",
		Text:  "text",
	}
	doc2 := &llmapp.Doc{
		Type: "related",
		// id "456" is not a URL, so it is omitted
		Title: "title2",
		Text:  "text2",
	}

	// This checks that the expected call to
	// [llmapp.Client.AnalyzeRelated] is made by [Analyze].
	ro, err := lc.AnalyzeRelated(ctx, doc1, []*llmapp.Doc{doc2})
	if err != nil {
		t.Fatal(err)
	}

	want := &Analysis{
		RelatedAnalysis: *ro,
	}

	if cmp.Diff(got, want, cmpopts.IgnoreFields(llmapp.Result{}, "Cached")) != "" {
		t.Errorf("Analyze() mismatch (-got +want):\n%s", cmp.Diff(got, want))
	}
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package search

import (
	"context"
	"fmt"

	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/llmapp"
	"golang.org/x/oscar/internal/storage"
)

// OverviewResult is the result of [Overview].
type OverviewResult struct {
	*llmapp.OverviewResult // the LLM-generated overview
}

// Overview returns an LLM-generated overview of a document and its related documents.
// id is the ID of the main document, which must be present in both the docs corpus and the vector db.
// Overview finds related documents using vector search (see [Vector]) with fixed options.
func Overview(ctx context.Context, lc *llmapp.Client, vdb storage.VectorDB, dc *docs.Corpus, id string) (*OverviewResult, error) {
	doc, ok := llmDoc(dc, "main", id)
	if !ok {
		return nil, fmt.Errorf("search.Overview: main doc %q not in docs corpus", id)
	}
	rs, err := searchRelated(vdb, dc, id)
	if err != nil {
		return nil, err
	}
	var related []*llmapp.Doc
	for _, r := range rs {
		d, ok := llmDoc(dc, "related", r.ID)
		if !ok {
			return nil, fmt.Errorf("search.Overview: related doc %s not in docs corpus", id)
		}
		related = append(related, d)
	}
	overview, err := lc.RelatedOverview(ctx, doc, related)
	if err != nil {
		return nil, err
	}
	return &OverviewResult{overview}, nil
}

var maxResults = 5

// searchRelated finds up to [maxResults] documents related to the document
// identified by id in vdb.
func searchRelated(vdb storage.VectorDB, dc *docs.Corpus, id string) ([]Result, error) {
	v, ok := vdb.Get(id)
	if !ok {
		return nil, fmt.Errorf("search: main doc %q not in vector db", id)
	}
	rs := Vector(vdb, dc, &VectorRequest{
		Options: Options{
			Limit: maxResults + 1, // buffer for self
		},
		Vector: v,
	})
	// Remove the query itself if present.
	if len(rs) > 0 && rs[0].ID == id {
		rs = rs[1:]
	}
	// Trim length.
	if len(rs) > maxResults {
		rs = rs[:maxResults]
	}
	return rs, nil
}

// llmDoc converts the document in dc identified by id into
// an [*llmapp.Doc] with type t.
// If the id is not in the corpus, it returns (nil, false).
func llmDoc(dc *docs.Corpus, t, id string) (*llmapp.Doc, bool) {
	d, ok := dc.Get(id)
	if !ok {
		return nil, false
	}
	doc := &llmapp.Doc{
		Type:  t,
		Title: d.Title,
		Text:  d.Text,
	}
	if isURL(d.ID) {
		doc.URL = d.ID
	}
	return doc, true
}

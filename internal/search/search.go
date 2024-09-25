// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package search performs nearest neigbors searches over
// vector databases, allowing the caller to specify filters
// for the results.
package search

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"path"
	"strings"

	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
)

// QueryRequest is a [Query] request.
// It includes the document to search for neighbors of, and
// (optional) result filters.
type QueryRequest struct {
	Options
	llm.EmbedDoc
}

// Options are the results filters that can be passed to the search
// functions as part of a [QueryRequest] or [VectorRequest].
type Options struct {
	Threshold float64 // lowest score to keep; default 0. Max is 1.
	Limit     int     // max results (fewer if Threshold is set); 0 means use a fixed default
}

// Result is a single result of a search ([Query] or [Vector]).
// It represents a single document in a vector database which is a
// nearest neighbor of the request.
type Result struct {
	Kind  string // kind of document: issue, doc page, etc.
	Title string
	storage.VectorResult
}

// Query performs a nearest neighbors search for the request's document
// over the given vector database, respecting the options set in [QueryRequest].
//
// It embeds the request's document onto the vector space using the given embedder.
//
// It expects that vdb is a vector database containing embeddings of
// the documents in dc, embedded using embed.
func Query(ctx context.Context, vdb storage.VectorDB, dc *docs.Corpus, embed llm.Embedder, req *QueryRequest) ([]Result, error) {
	vecs, err := embed.EmbedDocs(ctx, []llm.EmbedDoc{req.EmbedDoc})
	if err != nil {
		return nil, fmt.Errorf("EmbedDocs: %w", err)
	}
	vec := vecs[0]
	return vector(vdb, dc, vec, &req.Options), nil
}

// VectorRequest is a [Vector] request.
// It includes the vector to search for neighbors of, and
// (optional) result filters.
type VectorRequest struct {
	Options
	llm.Vector
}

// Vector performs a nearest neighbors search for the request's vector
// over the given vector database, respecting the options set in [VectorRequest].
//
// It expects that vdb is a vector database containing embeddings of
// the documents in dc, embedded using the same embedder used to create
// the request's vector.
func Vector(vdb storage.VectorDB, dc *docs.Corpus, req *VectorRequest) []Result {
	return vector(vdb, dc, req.Vector, &req.Options)
}

func vector(vdb storage.VectorDB, dc *docs.Corpus, vec llm.Vector, opts *Options) []Result {
	limit := defaultLimit
	if opts.Limit > 0 {
		limit = opts.Limit
	}
	// Search uses normalized dot product, so higher numbers are better.
	// Max is 1, min is 0.
	threshold := 0.0
	if opts.Threshold > 0 {
		threshold = opts.Threshold
	}
	var srs []Result
	for _, r := range vdb.Search(vec, limit) {
		if r.Score < threshold {
			break
		}
		title := ""
		if d, ok := dc.Get(r.ID); ok {
			title = d.Title
		}
		srs = append(srs, Result{
			Kind:         docIDKind(r.ID),
			Title:        title,
			VectorResult: r,
		})
	}
	return srs
}

// Round rounds r.Score to three decimal places.
func (r *Result) Round() {
	r.Score = math.Round(r.Score*1e3) / 1e3
}

// Maximum number of search results to return by default.
const defaultLimit = 20

// docIDKind determines the kind of document from its ID.
// It returns the empty string if it cannot do so.
func docIDKind(id string) string {
	u, err := url.Parse(id)
	if err != nil {
		return ""
	}
	hp := path.Join(u.Host, u.Path)
	switch {
	case strings.HasPrefix(hp, "github.com/golang/go/issues/"):
		return "GitHubIssue"
	case strings.HasPrefix(hp, "go.dev/wiki/"):
		return "GoWiki"
	case strings.HasPrefix(hp, "go.dev/doc/"):
		return "GoDocumentation"
	case strings.HasPrefix(hp, "go.dev/ref/"):
		return "GoReference"
	case strings.HasPrefix(hp, "go.dev/blog/"):
		return "GoBlog"
	case strings.HasPrefix(hp, "go.dev/"):
		return "GoDevPage"
	default:
		return ""
	}
}

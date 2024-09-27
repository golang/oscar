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
	"regexp"
	"strings"
	"testing"

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
	Threshold float64  // lowest score to keep; default 0. Max is 1.
	Limit     int      // max results (fewer if Threshold is set); 0 means use a fixed default
	AllowKind []string // kinds of documents to keep; empty means keep all
	DenyKind  []string // kinds of documents to remove; empty means remove none
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
	// By defaut, allow all kinds of documents.
	allowKind := func(string) bool { return true }
	if len(opts.AllowKind) != 0 {
		allowKind = containsFunc(opts.AllowKind)
	}
	// By defaut, deny no kinds of documents.
	denyKind := func(string) bool { return false }
	if len(opts.DenyKind) != 0 {
		denyKind = containsFunc(opts.DenyKind)
	}
	var srs []Result
	for _, r := range vdb.Search(vec, limit) {
		if r.Score < threshold {
			break
		}
		kind := docIDKind(r.ID)
		if !allowKind(kind) || denyKind(kind) {
			continue
		}
		title := ""
		if d, ok := dc.Get(r.ID); ok {
			title = d.Title
		}
		srs = append(srs, Result{
			Kind:         kind,
			Title:        title,
			VectorResult: r,
		})
	}
	return srs
}

func containsFunc(s []string) func(string) bool {
	m := make(map[string]bool)
	for _, k := range s {
		m[k] = true
	}
	return func(s string) bool { return m[s] }
}

// Round rounds r.Score to three decimal places.
func (r *Result) Round() {
	r.Score = math.Round(r.Score*1e3) / 1e3
}

// IDIsURL reports whether the Result's ID is a valid URL.
func (r *Result) IDIsURL() bool {
	_, err := url.Parse(r.ID)
	return err == nil
}

// Maximum number of search results to return by default.
const defaultLimit = 20

// Recognized kinds of documents.
const (
	KindGitHubIssue     = "GitHubIssue"
	KindGoWiki          = "GoWiki"
	KindGoDocumentation = "GoDocumentation"
	KindGoReference     = "GoReference"
	KindGoBlog          = "GoBlog"
	KindGoDevPage       = "GoDevPage"
	// Unknown document.
	KindUnknown = "Unknown"
)

// docIDKind determines the kind of document from its ID.
// It returns the empty string if it cannot do so.
//
// The function assumes that we only care about the Go project.
func docIDKind(id string) string {
	u, err := url.Parse(id)
	if err != nil {
		return KindUnknown
	}
	hp := path.Join(u.Host, u.Path)
	switch {
	case strings.HasPrefix(hp, "github.com/golang/go/issues/"):
		return KindGitHubIssue
	case strings.HasPrefix(hp, "go.dev/wiki/"):
		return KindGoWiki
	case strings.HasPrefix(hp, "go.dev/doc/"):
		return KindGoDocumentation
	case strings.HasPrefix(hp, "go.dev/ref/"):
		return KindGoReference
	case strings.HasPrefix(hp, "go.dev/blog/"):
		return KindGoBlog
	case strings.HasPrefix(hp, "go.dev/"):
		return KindGoDevPage
	default:
		// In tests, any GitHub project's issues are OK.
		if testing.Testing() && githubIssueRE.MatchString(hp) {
			return KindGitHubIssue
		}
		return KindUnknown
	}
}

// Matches GitHub issue URLs in any project, e.g. github.com/golang/go/issues/42.
var githubIssueRE = regexp.MustCompile(`^github\.com/[\w-]+/[\w-]+/issues/\d+$`)

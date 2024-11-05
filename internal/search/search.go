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
//
// TODO(tatianabradley): Make kinds case insensitive.
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

// Validate returns an error if any of the options is invalid.
func (o *Options) Validate() error {
	if o.Limit < 0 {
		return fmt.Errorf("limit must be >= 0 (got: %d)", o.Limit)
	}
	if o.Threshold < 0 || o.Threshold > 1 {
		return fmt.Errorf("threshold must be >= 0 and <= 1 (got: %.3f)", o.Threshold)
	}
	for _, allow := range o.AllowKind {
		if _, ok := kinds[allow]; !ok {
			return fmt.Errorf("unrecognized allow kind %q (case-sensitive)", allow)
		}
	}
	for _, deny := range o.DenyKind {
		if _, ok := kinds[deny]; !ok {
			return fmt.Errorf("unrecognized deny kind %q (case-sensitive)", deny)
		}
	}
	return nil
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

// IDIsURL reports whether the Result's ID is a valid absolute URL.
func (r *Result) IDIsURL() bool {
	return isURL(r.ID)
}

// isURL reports whether the string is a valid absolute URL.
func isURL(s string) bool {
	// Use [url.ParseRequestURI] as it only accepts absolute URLs
	// ([url.Parse] accepts relative URLs too).
	_, err := url.ParseRequestURI(s)
	return err == nil
}

// Maximum number of search results to return by default.
const defaultLimit = 20

// Recognized kinds of documents.
const (
	KindGitHubIssue             = "GitHubIssue"
	KindGitHubDiscussion        = "GitHubDiscussion"
	KindGoWiki                  = "GoWiki"
	KindGoDocumentation         = "GoDocumentation"
	KindGoReference             = "GoReference"
	KindGoBlog                  = "GoBlog"
	KindGoDevPage               = "GoDevPage"
	KindGoGerritChange          = "GoGerritChange"
	KindGoogleGroupConversation = "GoogleGroupsConversation"
	// Unknown document.
	KindUnknown = "Unknown"
)

// Set of recognized document kinds.
var kinds = map[string]bool{
	KindGitHubIssue:             true,
	KindGitHubDiscussion:        true,
	KindGoWiki:                  true,
	KindGoDocumentation:         true,
	KindGoBlog:                  true,
	KindGoDevPage:               true,
	KindUnknown:                 true,
	KindGoGerritChange:          true,
	KindGoogleGroupConversation: true,
}

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
	case githubRE.MatchString(hp):
		return githubKind(hp, u.Fragment)
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
	case strings.HasPrefix(hp, "go-review.googlesource.com/"):
		return KindGoGerritChange
	case goGoogleGroupConversation(hp):
		return KindGoogleGroupConversation
	}
	return KindUnknown
}

func githubKind(hostPath string, fragment string) string {
	// We don't currently recognize Github URLs with fragments.
	if fragment != "" {
		return KindUnknown
	}

	s := githubRE.FindStringSubmatch(hostPath)
	if len(s) != 3 { // malformed
		return KindUnknown
	}
	project, api := s[1], s[2]

	// Project must be "golang/go", except in tests.
	if project != "golang/go" && !testing.Testing() {
		return KindUnknown
	}

	switch api {
	case "issues":
		return KindGitHubIssue
	case "discussions":
		return KindGitHubDiscussion
	default:
		return KindUnknown
	}
}

// Matches GitHub URLs in any project of the form github.com/owner/repo/api/num.
var githubRE = regexp.MustCompile(`^github\.com/([\w-]+/[\w-]+)/([\w-]+)/\d+$`)

func goGoogleGroupConversation(hostPath string) bool {
	s := googleGroupRE.FindStringSubmatch(hostPath)
	if len(s) != 2 { // malformed
		return false
	}

	// Group must be "golang-*".
	if !strings.HasPrefix(s[1], "golang-") {
		return false
	}
	return true
}

// Matches Google Groups conversation URLs for any group of the
// form groups.google.com/g/group/c/conversation.
var googleGroupRE = regexp.MustCompile(`^groups\.google\.com/g/([\w-]+)/c/[\w-]+$`)

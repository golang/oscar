// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/safehtml/template"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/search"
)

// a searchPage holds the fields needed to display the results
// of a search.
type searchPage struct {
	CommonPage

	Params  searchParams    // the raw query parameters
	Results []search.Result // the search results to display
	Error   error           // if non-nil, the error to display instead of results
}

func (g *Gaby) handleSearch(w http.ResponseWriter, r *http.Request) {
	handlePage(w, g.populateSearchPage(r), searchPageTmpl)
}

func handlePage(w http.ResponseWriter, p page, tmpl *template.Template) {
	b, err := Exec(tmpl, p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(b)
}

// populateSearchPage returns the contents of the vector search page.
func (g *Gaby) populateSearchPage(r *http.Request) *searchPage {
	var pm searchParams
	pm.parseParams(r)
	p := &searchPage{
		Params: pm,
	}
	p.setCommonPage()
	opts, err := pm.toOptions()
	if err != nil {
		p.Error = fmt.Errorf("invalid form value: %w", err)
		return p
	}
	q := trim(pm.Query)
	results, err := g.search(r.Context(), q, *opts)
	if err != nil {
		p.Error = fmt.Errorf("search: %w", err)
		return p
	}
	p.Results = results
	return p
}

// search performs a search on the query and options.
//
// If the query is an exact match for an ID in the vector database,
// it looks up the vector for that ID and performs a search for the
// nearest neighbors of that vector.
// Otherwise, it embeds the query and performs a nearest neighbor
// search for the embedding.
//
// It returns an error if search fails.
func (g *Gaby) search(ctx context.Context, q string, opts search.Options) (results []search.Result, err error) {
	if q == "" {
		return nil, nil
	}

	if vec, ok := g.vector.Get(q); ok {
		results = search.Vector(g.vector, g.docs,
			&search.VectorRequest{
				Options: opts,
				Vector:  vec,
			})
	} else {
		if results, err = search.Query(ctx, g.vector, g.docs, g.embed,
			&search.QueryRequest{
				EmbedDoc: llm.EmbedDoc{Text: q},
				Options:  opts,
			}); err != nil {
			return nil, err
		}
	}

	for i := range results {
		results[i].Round()
	}

	return results, nil
}

// searchParams holds the raw query parameters.
type searchParams struct {
	Query string // a text query, or an ID of a document in the database

	// String representations of the fields of [search.Options]
	Threshold   string
	Limit       string
	Allow, Deny string // comma separated lists
}

// parseParams parses the query params from the request.
func (pm *searchParams) parseParams(r *http.Request) {
	pm.Query = r.FormValue(paramQuery)
	pm.Threshold = r.FormValue(paramThreshold)
	pm.Limit = r.FormValue(paramLimit)
	pm.Allow = r.FormValue(paramAllow)
	pm.Deny = r.FormValue(paramDeny)
}

func (p *searchPage) setCommonPage() {
	p.CommonPage = CommonPage{
		ID:          searchID,
		Description: "Search Oscar's database of GitHub issues, Go documentation, and other documents.",
		Form: Form{
			Inputs:     p.Params.inputs(),
			SubmitText: "search",
		},
	}
}

const (
	paramQuery     = "q"
	paramThreshold = "threshold"
	paramLimit     = "limit"
	paramAllow     = "allow_kind"
	paramDeny      = "deny_kind"
)

var (
	safeQuery     = toSafeID(paramQuery)
	safeThreshold = toSafeID(paramThreshold)
	safeLimit     = toSafeID(paramLimit)
	safeAllow     = toSafeID(paramAllow)
	safeDeny      = toSafeID(paramDeny)
)

// inputs converts the params into HTML form inputs.
func (pm *searchParams) inputs() []FormInput {
	return []FormInput{
		{

			Label:       "query",
			Type:        "string",
			Description: "the text to search for neigbors of OR the ID (usually a URL) of a document in the vector database",
			Name:        safeQuery,
			Required:    true,
			Typed: TextInput{
				ID:    safeQuery,
				Value: pm.Query,
			},
		},
		{

			Label:       "min similarity",
			Type:        "float64 between 0 and 1",
			Description: "similarity cutoff (default: 0, allow all)",
			Name:        safeThreshold,
			Typed: TextInput{
				ID:    safeThreshold,
				Value: pm.Threshold,
			},
		},
		{

			Label:       "max results",
			Type:        "int",
			Description: "maximum number of results to display (default: 20)",
			Name:        safeLimit,
			Typed: TextInput{
				ID:    safeLimit,
				Value: pm.Limit,
			},
		},
		{

			Label:       "include types",
			Type:        "comma-separated list",
			Description: "document types to include, e.g `GitHubIssue,GoBlog` (default: empty, include all)",
			Name:        safeAllow,
			Typed: TextInput{
				ID:    safeAllow,
				Value: pm.Allow,
			},
		},
		{

			Label:       "exclude types",
			Type:        "comma-separated list",
			Description: "document types to filter out, e.g `GitHubIssue,GoBlog` (default: empty, exclude none)",
			Name:        safeDeny,
			Typed: TextInput{
				ID:    safeDeny,
				Value: pm.Deny,
			},
		},
	}
}

var trim = strings.TrimSpace

// toSearchOptions converts a searchParams into a [search.Options],
// trimming any leading/trailing spaces.
//
// It returns an error if any of the form inputs is invalid.
func (f *searchParams) toOptions() (_ *search.Options, err error) {
	opts := &search.Options{}

	splitAndTrim := func(s string) []string {
		vs := strings.Split(s, ",")
		for i, v := range vs {
			vs[i] = trim(v)
		}
		return vs
	}

	if l := trim(f.Limit); l != "" {
		opts.Limit, err = strconv.Atoi(l)
		if err != nil {
			return nil, fmt.Errorf("limit: %w", err)
		}
	}

	if t := trim(f.Threshold); t != "" {
		opts.Threshold, err = strconv.ParseFloat(t, 64)
		if err != nil {
			return nil, fmt.Errorf("threshold: %w", err)
		}
	}

	if a := trim(f.Allow); a != "" {
		opts.AllowKind = splitAndTrim(a)
	}

	if d := trim(f.Deny); d != "" {
		opts.DenyKind = splitAndTrim(d)
	}

	if err := opts.Validate(); err != nil {
		return nil, err
	}

	return opts, nil
}

var searchPageTmpl = newTemplate(searchPageTmplFile, nil)

func (g *Gaby) handleSearchAPI(w http.ResponseWriter, r *http.Request) {
	sreq, err := readJSONBody[search.QueryRequest](r)
	if err != nil {
		// The error could also come from failing to read the body, but then the
		// connection is probably broken so it doesn't matter what status we send.
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sres, err := search.Query(r.Context(), g.vector, g.docs, g.embed, sreq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := json.Marshal(sres)
	if err != nil {
		http.Error(w, "json.Marshal: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}

func readJSONBody[T any](r *http.Request) (*T, error) {
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	t := new(T)
	if err := json.Unmarshal(data, t); err != nil {
		return nil, err
	}
	return t, nil
}

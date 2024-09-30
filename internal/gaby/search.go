// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/search"
)

// a searchPage holds the fields needed to display the results
// of a search.
type searchPage struct {
	searchForm                  // the raw query and options
	Results     []search.Result // the search results to display
	SearchError string          // if non-empty, the error to display instead of results
}

func (g *Gaby) handleSearch(w http.ResponseWriter, r *http.Request) {
	page := g.populatePage(r)
	var buf bytes.Buffer
	if err := searchPageTmpl.Execute(&buf, page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(buf.Bytes())
}

// populatePage returns the contents of the vector search page.
func (g *Gaby) populatePage(r *http.Request) searchPage {
	form := searchForm{
		Query:     r.FormValue("q"),
		Threshold: r.FormValue("threshold"),
		Limit:     r.FormValue("limit"),
		Allow:     r.FormValue("allow_kind"),
		Deny:      r.FormValue("deny_kind"),
	}
	opts, err := form.toOptions()
	if err != nil {
		return searchPage{
			searchForm:  form,
			SearchError: fmt.Errorf("invalid form value: %w", err).Error(),
		}
	}
	q := strings.TrimSpace(form.Query)
	results, err := g.search(r.Context(), q, *opts)
	if err != nil {
		return searchPage{
			searchForm:  form,
			SearchError: fmt.Errorf("search: %w", err).Error(),
		}
	}
	return searchPage{
		searchForm: form,
		Results:    results,
	}
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

// searchForm holds the raw inputs to the search form.
type searchForm struct {
	Query string // a text query, or an ID of a document in the database

	// String representations of the fields of [search.Options]
	Threshold   string
	Limit       string
	Allow, Deny string // comma separated lists
}

// toOptions converts a searchForm into a [search.Options],
// trimming any leading/trailing spaces.
//
// It returns an error if any of the form inputs is invalid.
func (f *searchForm) toOptions() (_ *search.Options, err error) {
	opts := &search.Options{}

	trim := strings.TrimSpace
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

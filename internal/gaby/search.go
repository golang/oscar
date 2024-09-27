// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/search"
)

type searchPage struct {
	Query string
	search.Options
	// allowlist and denylist as comma-separated strings (for display)
	AllowStr, DenyStr string
	Results           []search.Result
}

func (g *Gaby) handleSearch(w http.ResponseWriter, r *http.Request) {
	data, err := g.doSearch(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		_, _ = w.Write(data)
	}
}

// doSearch returns the contents of the vector search page.
func (g *Gaby) doSearch(r *http.Request) ([]byte, error) {
	page := populatePage(r)
	if page.Query != "" {
		var err error
		page.Results, err = search.Query(r.Context(), g.vector, g.docs, g.embed,
			&search.QueryRequest{
				EmbedDoc: llm.EmbedDoc{Text: page.Query},
				Options:  page.Options,
			})
		if err != nil {
			return nil, err
		}
		for i := range page.Results {
			page.Results[i].Round()
		}
	}
	var buf bytes.Buffer
	if err := searchPageTmpl.Execute(&buf, page); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// populatePage parses the form values into a searchPage.
// TODO(tatianabradley): Add error handling for malformed
// filters and trim spaces in inputs.
func populatePage(r *http.Request) searchPage {
	threshold, err := strconv.ParseFloat(r.FormValue("threshold"), 64)
	if err != nil {
		threshold = 0
	}
	limit, err := strconv.Atoi(r.FormValue("limit"))
	if err != nil {
		limit = 20
	}
	var allow, deny []string
	var allowStr, denyStr string
	if allowStr = r.FormValue("allow_kind"); allowStr != "" {
		allow = strings.Split(allowStr, ",")
	}
	if denyStr = r.FormValue("deny_kind"); denyStr != "" {
		deny = strings.Split(denyStr, ",")
	}
	return searchPage{
		Query: r.FormValue("q"),
		Options: search.Options{
			Limit:     limit,
			Threshold: threshold,
			AllowKind: allow,
			DenyKind:  deny,
		},
		AllowStr: allowStr,
		DenyStr:  denyStr,
	}
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

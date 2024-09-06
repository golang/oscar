// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"

	"github.com/google/safehtml/template"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
)

type searchPage struct {
	Query   string
	Results []searchResult
}

type searchResult struct {
	Title   string
	VResult storage.VectorResult
	IDIsURL bool // VResult.ID can be parsed as a URL
}

func (g *Gaby) handleSearch(w http.ResponseWriter, r *http.Request) {
	if err := g.doSearch(w, r); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// doSearch displays the vector search page. If an error occurs,
// it returns the error before writing anything to w.
func (g *Gaby) doSearch(w http.ResponseWriter, r *http.Request) error {
	page := searchPage{
		Query: r.FormValue("q"),
	}
	if page.Query != "" {
		var err error
		page.Results, err = g.search(page.Query)
		if err != nil {
			return err
		}
		// Round scores to three decimal places.
		const r = 1e3
		for i := range page.Results {
			sp := &page.Results[i].VResult.Score
			*sp = math.Round(*sp*r) / r
		}
	}
	var buf bytes.Buffer
	if err := searchPageTmpl.Execute(&buf, page); err != nil {
		return err
	}
	_, _ = w.Write(buf.Bytes())
	return nil
}

// Maximum number of search results to return.
const maxResults = 20

// search does a search for query over Gaby's vector database.
func (g *Gaby) search(query string) ([]searchResult, error) {
	vecs, err := g.embed.EmbedDocs(context.Background(), []llm.EmbedDoc{{Title: "", Text: query}})
	if err != nil {
		return nil, fmt.Errorf("EmbedDocs: %w", err)
	}
	vec := vecs[0]
	var srs []searchResult
	for _, r := range g.vector.Search(vec, maxResults) {
		title := "?"
		if d, ok := g.docs.Get(r.ID); ok {
			title = d.Title
		}
		_, err := url.Parse(r.ID)
		srs = append(srs, searchResult{title, r, err == nil})
	}
	return srs, nil
}

var searchPageTmpl = template.Must(template.New("").Parse(`
<!doctype html>
<html>
  <head>
    <title>Oscar Search</title>
  </head>
  <body>
    <h1>Gaby search</h1>
    <p>Search Gaby's database of GitHub issues and Go documentation.</p>
    <form action="/search" method="GET">
      <input type="text" name="q" value="{{.Query}}" required autofocus />
      <input type="submit" value="Search"/>
    </form>

	{{with .Results -}}
	  {{- range . -}}
	    <p>{{with .Title}}{{.}}: {{end -}}
	    {{if .IDIsURL -}}
	      {{with .VResult}}<a href="{{.ID}}">{{.ID}}</a>{{end -}}
	    {{else -}}
	      {{.VResult.ID -}}
	    {{end -}}
	    {{" "}}({{.VResult.Score}})</p>
	  {{end}}
	{{- else -}}
	 {{if .Query}}No results.{{end}}
  	{{- end}}
  </body>
</html>
`))

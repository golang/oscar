// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/google/safehtml/template"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
)

func (g *Gaby) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.FormValue("q")
	if query == "" {
		_, _ = io.WriteString(w, searchForm)
		return
	}
	results, err := g.search(query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := searchResultsHTML(query, results)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}

type searchResult struct {
	Title   string
	VResult storage.VectorResult
	IDIsURL bool // VResult.ID can be parsed as a URL
}

func (g *Gaby) search(query string) ([]searchResult, error) {
	vecs, err := g.embed.EmbedDocs(context.Background(), []llm.EmbedDoc{{Title: "", Text: query}})
	if err != nil {
		return nil, fmt.Errorf("EmbedDocs: %w", err)
	}
	vec := vecs[0]
	var srs []searchResult
	for _, r := range g.vector.Search(vec, 20) {
		title := "?"
		if d, ok := g.docs.Get(r.ID); ok {
			title = d.Title
		}
		_, err := url.Parse(r.ID)
		srs = append(srs, searchResult{title, r, err == nil})
	}
	return srs, nil
}

func searchResultsHTML(query string, results []searchResult) ([]byte, error) {
	data := struct {
		Query   string
		Results []searchResult
	}{
		query, results,
	}
	var buf bytes.Buffer
	if err := searchResultsTmpl.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

const searchForm = `
<!doctype html>
<html>
  <head>
    <title>Oscar Search</title>
    <!-- All links open in another tab. -->
    <base target="_blank">
  </head>
  <body>
    <h1>Vector search</h1>
    <form action="/search" method="GET">
      <input type="text" name="q" required autofocus />
      <input type="submit" value="Search"/>
    </form
  </body>
</html>
`

var searchResultsTmpl = template.Must(template.New("").Parse(`
<!doctype html>
<html>
  <head>
    <title>Oscar Search Results</title>
  </head>
  <body>
  <h1>Search results for "{{.Query}}"</h1>
  {{- with .Results -}}
	  {{- range . }}
	     <p>{{with .Title}}{{.}}: {{end -}}
	    {{if .IDIsURL -}}
	      {{with .VResult}}<a href="{{.ID}}">{{.ID}}</a>{{end -}}
	    {{else -}}
	      {{.VResult.ID}}
	    {{end -}}
	    {{" "}}({{.VResult.Score}})</p>
	  {{end}}
  {{- else -}}
     No results.
  {{- end}}
  </body>
</html>
`))

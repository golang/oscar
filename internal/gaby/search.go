// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"text/template"

	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/search"
)

type searchPage struct {
	Query   string
	Results []search.Result
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
	page := searchPage{
		Query: r.FormValue("q"),
	}
	if page.Query != "" {
		var err error
		page.Results, err = search.Query(r.Context(), g.vector, g.docs, g.embed,
			&search.QueryRequest{
				EmbedDoc: llm.EmbedDoc{Text: page.Query},
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

// This template assumes that if a result's Kind is non-empty, it is a URL,
// and vice versa.
var searchPageTmpl = template.Must(template.New("search").Parse(`
<!doctype html>
<html>
  <head>
    <title>Oscar Search</title>
  </head>
  <body>
    <h1>Gaby search</h1>
    <p>Search Gaby's database of GitHub issues and Go documentation.</p>
    <form id="form" action="/search" method="GET">
      <input type="text" name="q" value="{{.Query}}" required autofocus />
      <input type="submit" value="Search"/>
    </form>

    <div id="working"></div>

    <script>
    const form = document.getElementById("form");
    form.addEventListener("submit", (event) => {
		document.getElementById("working").innerHTML = "<p style='margin-top:1rem'>Working...</p>"
    })
    </script>

	{{with .Results -}}
	  {{- range . -}}
	    <p>{{with .Title}}{{.}}: {{end -}}
	    {{if .Kind -}}
	      <a href="{{.ID}}">{{.ID}}</a>
	    {{else -}}
	      {{.ID -}}
	    {{end -}}
	    {{" "}}({{.Score}})</p>
	  {{end}}
	{{- else -}}
	 {{if .Query}}No results.{{end}}
  	{{- end}}
  </body>
</html>
`))

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

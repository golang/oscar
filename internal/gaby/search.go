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
	"math"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/google/safehtml/template"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
)

type searchPage struct {
	Query   string
	Results []searchResult
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
		page.Results, err = g.search(r.Context(), &searchRequest{EmbedDoc: llm.EmbedDoc{Text: page.Query}})
		if err != nil {
			return nil, err
		}
		for i := range page.Results {
			page.Results[i].round()
		}
	}
	var buf bytes.Buffer
	if err := searchPageTmpl.Execute(&buf, page); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type searchRequest struct {
	Threshold float64 // lowest score to keep; default 0. Max is 1.
	Limit     int     // max results (fewer if Threshold is set); 0 means use a fixed default
	llm.EmbedDoc
}
type searchResult struct {
	Kind  string // kind of document: issue, doc page, etc.
	Title string
	storage.VectorResult
}

// Round rounds r.Score to three decimal places.
func (r *searchResult) round() {
	r.Score = math.Round(r.Score*1e3) / 1e3
}

// Maximum number of search results to return by default.
const defaultLimit = 20

// search does a search for query over Gaby's vector database.
func (g *Gaby) search(ctx context.Context, sreq *searchRequest) ([]searchResult, error) {
	vecs, err := g.embed.EmbedDocs(ctx, []llm.EmbedDoc{sreq.EmbedDoc})
	if err != nil {
		return nil, fmt.Errorf("EmbedDocs: %w", err)
	}
	vec := vecs[0]

	limit := defaultLimit
	if sreq.Limit > 0 {
		limit = sreq.Limit
	}
	// Search uses normalized dot product, so higher numbers are better.
	// Max is 1, min is 0.
	threshold := 0.0
	if sreq.Threshold > 0 {
		threshold = sreq.Threshold
	}

	var srs []searchResult
	for _, r := range g.vector.Search(vec, limit) {
		if r.Score < threshold {
			break
		}
		title := ""
		if d, ok := g.docs.Get(r.ID); ok {
			title = d.Title
		}
		srs = append(srs, searchResult{
			Kind:         docIDKind(r.ID),
			Title:        title,
			VectorResult: r,
		})
	}
	return srs, nil
}

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

// This template assumes that if a result's Kind is non-empty, it is a URL,
// and vice versa.
var searchPageTmpl = template.Must(template.New("").Parse(`
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
	sreq, err := readJSONBody[searchRequest](r)
	if err != nil {
		// The error could also come from failing to read the body, but then the
		// connection is probably broken so it doesn't matter what status we send.
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sres, err := g.search(r.Context(), sreq)
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

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

	"github.com/google/safehtml/template"

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

// This template assumes that if a result's Kind is non-empty, it is a URL,
// and vice versa.
var searchPageTmpl = template.Must(template.New("search").Parse(`
<!doctype html>
<html>
  <head>
    <title>Oscar Search</title>
	<style>
	body {
		font-family: sans-serif;
		font-size: 1em;
		color: #3e4042;
	}
	.header {
		display: block;
	}
	form span {
		display: block;
		padding-bottom: .2em
	}
	label {
		display: inline-block;
		width: 20%;
		margin-right: .1em;
	}
	input {
		display: inline-block;
		width: 20%;
	}
	input.submit {
		width: 10%;
	}
	.title,span.title a {
		font-weight: bold;
		font-size: 1.1em;
		color: #3e4042;
	}
	.kind,.score {
		color: #6e7072;
		font-size: .75em;
	}
	a {
		color: #007d9c;
		text-decoration: none;
	}
	p {
		margin-top: .25em;
		margin-bottom: .25em;
	}
	a:hover {
		text-decoration: underline;
	}
	div.result span {
		display: block;
		padding-bottom: .05em;
	}
	div.result {
		padding-bottom: 1em;
	}
	div.section {
		padding: 0em 2em 1em 1em;
	}
	.filter-tips-box {
		font-size: .75em;
		padding-bottom: .5em;
	}
	.toggle {
	    font-weight: bold;
	 	color: #007d9c;
	}
	.toggle:hover {
		text-decoration: underline;
	}
	#filter-tips {
		display: none;
	}
	.submit {
		padding-top: .5em;
	}
	</style>
  </head>
  <body>
    <div class="section" class="header">
	 <h1>Gaby search</h1>
		<p>Search Gaby's database of GitHub issues and Go documentation.</p>
		<div class="filter-tips-box">
			<div class="toggle" onclick="toggle()">[show/hide filter tips]</div>
			<ul id="filter-tips">
				<li><b>min similarity</b> (<code>float64</code> between 0 and 1): similarity cutoff (default: 0, allow all)</li>
				<li><b>max results</b> (<code>int</code>): maximum number of results to display (default: 20)</li>
				<li><b>include types</b> (comma-separated list): document types to include, e.g <code>GitHubIssue,GoBlog</code> (default: empty, include all)</li>
				<li><b>exclude types</b> (comma-separated list): document types to filter out, e.g <code>GitHubIssue,GoBlog</code> (default: empty, exclude none)</li>
			</ul>
		</div>
	 <form id="form" action="/search" method="GET">
		<span>
		 <label for="query"><b>query</b></label>
		 <input id="query" type="text" name="q" value="{{.Query}}" required autofocus />
		</span>
		<span>
		 <label for="threshold">min similarity</label>
		 <input id="threshold" type="text" name="threshold" value="{{.Threshold}}" required autofocus />
		</span>
		<span>
		 <label for="limit">max results</label>
		 <input id="limit" type="text" name="limit" value="{{.Limit}}" required autofocus />
		</span>
		<span>
		 <label for="allow_kind">allow types</label>
		 <input id="allow_kind" type="text" name="allow_kind" value="{{.AllowStr}}" optional autofocus />
		</span>
		<span>
		 <label for="deny_kind">exclude types</code></label>
		 <input id="deny_kind" type="text" name="deny_kind" value="{{.DenyStr}}" optional autofocus />
		</span>
		<span class="submit">
		 <input type="submit" value="search"/>
		</span>
	 </form>
	</div>

    <script>
    const form = document.getElementById("form");
    form.addEventListener("submit", (event) => {
		document.getElementById("working").innerHTML = "<p style='margin-top:1rem'>Working...</p>"
    })
	function toggle() {
		var x = document.getElementById("filter-tips");
		if (x.style.display === "block") {
			x.style.display = "none";
		} else {
			x.style.display = "block";
		}
	}
    </script>

	<div class="section">
	<div id="working"></div>
	{{with .Results -}}
	  {{- range . -}}
	    <div class="result">
	    {{if .Kind -}}
			 <span><a class="id" href="{{.ID}}">{{.ID}}</a></span>
			{{if .Title -}}
			 <span class="title"><a href="{{.ID}}">{{.Title}}</a></span>
			{{end -}}
			<span class="kind">type: {{.Kind}}</span>
	    {{else -}}
		  <span class="id">{{.ID -}}</span>
		  {{with .Title}}
			<span class="title">>{{.}}</span>
		  {{end -}}
	    {{end -}}
	    <span class="score">similarity: <b>{{.Score}}</b><span>
		</div>
	  {{end}}
	{{- else -}}
	 {{if .Query}}<p>No results.</p>{{end}}
  	{{- end}}
   </div>
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

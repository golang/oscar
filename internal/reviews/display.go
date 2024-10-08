// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package reviews

import (
	"html/template"
	"io"
	"log/slog"
	"net/http"
)

// Display is an HTTP handler function that displays the
// changes to review.
func Display(lg *slog.Logger, doc template.HTML, cps []ChangePreds, w http.ResponseWriter, r *http.Request) {
	if len(cps) == 0 {
		io.WriteString(w, reportNoData)
		return
	}

	sc := make([]CP, len(cps))
	for i, v := range cps {
		sc[i] = CP{v}
	}

	data := &displayType{
		Doc:     doc,
		Changes: sc,
	}
	if err := displayTemplate.Execute(w, data); err != nil {
		lg.Error("template execution failed", "err", err)
	}
}

// reportNoData is the HTML that we report if we have not finished
// applying predicates to changes.
const reportNoData = `
<!DOCTYPE html>
<html lang="en">
<meta http-equiv="refresh" content="30">
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
  <head>
    <title>Collecting Data...</title>
  </head>
  <body>
    <h1>Please wait</h1>
    Still collecting data to report...
  </body>
</html>
`

// displayTemplate is used to display the changes.
var displayTemplate = template.Must(template.New("display").Parse(displayHTML))

// displayType is the type expected by displayHTML
type displayType struct {
	Doc     template.HTML
	Changes []CP
}

// CP is the type we pass to the HTML template displayTemplate.
type CP struct {
	ChangePreds
}

// FormattedLastUpdate returns the time of the last update as YYYY-MM-DD.
func (cp *CP) FormattedLastUpdate() string {
	return cp.Change.Updated().Format("2006-01-02")
}

// displayHTML is the web page we display. This is HTML template source.
// Loosely copied from golang.org/x/build/devapp/template/reviews.tmpl.
const displayHTML = `
<!DOCTYPE html>
<html lang="en">
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
* {
  box-sizing: border-box;
  margin: 0;
  padding: 0;
}
body {
  font: 13px system-ui, sans-serif;
  padding: 1rem;
}
h2 {
  margin: 1em 0 .35em;
}
h3:first-of-type {
  padding: 0
}
a:link,
a:visited {
  color: #00c;
}
header {
  border-bottom: 1px solid #666;
  margin-bottom: 10px;
  padding-bottom: 10px;
}
.header-subtitle {
  color: #666;
  font-size: .9em;
}
.row {
  border-bottom: 1px solid #f1f2f3;
  display: flex;
  padding: .5em 0;
  white-space: nowrap;
}
.date {
  min-width: 6rem;
}
.owner {
  min-width: 10rem;
  max-width: 10rem;
  overflow: hidden;
  text-overflow: ellipsis;
  padding-right: 1em;
}
.predicates {
  margin-left: 6rem;
  font-size: .9em;
}
.highscore {
  color: #fff;
  display: inline-block;
  border-radius: 2px;
  padding: 2px 5px;
  text-decoration: none;
  background-color: #2e7d32;
}
.posscore {
  color: #000;
  display: inline-block;
  border-radius: 2px;
  padding: 2px 5px;
  text-decoration: none;
  background-color: #dcedc8;
}
.negscore {
  color: #000;
  display: inline-block;
  border-radius: 2px;
  padding: 2px 5px;
  text-decoration: none;
  background-color: #fdaeb7;
}
.lowscore {
  color: #fff;
  display: inline-block;
  border-radius: 2px;
  padding: 2px 5px;
  text-decoration: none;
  background-color: #b71c1c;
}
.zeroscore {
  color: #000;
  display: inline-block;
  border-radius: 2px;
  padding: 2px 5px;
  text-decoration: none;
  background-color: #e0e0e0;
}
.icons,
.number {
  flex-shrink: 0;
}
.icons {
  height: 20px;
  margin-right: 1.5em;
  text-align: right;
  width: 12em;
}
.number {
  margin-right: 1.5em;
  text-align: right;
  width: 6ch;
}
.subject {
  overflow: hidden;
  text-overflow: ellipsis;
}
[hidden] {
  display: none;
}
</style>
  <head>
    <title>Open Go Code Reviews</title>
  </head>
  <body>
    <header>
      <strong>Open changes</strong>
      <div class="header-subtitle">
        {{.Doc}}
      </div>
      <div class="header-subtitle">
        <a href="https://go.googlesource.com/oscar/+/master/internal/goreviews/display.go">Source code</a>
      </div>
    </header>
  {{range $change := .Changes}}
    <div class="row">
      <span class="date">{{.FormattedLastUpdate}}</span>
      <span class="owner">{{.Change.Author.DisplayName}}</span>
      <a class="number" href="https://go.dev/cl/{{.Change.ID}}" target="_blank">{{.Change.ID}}</a>
      <span class="subject">{{.Change.Subject}}</span>
      <span class="predicates">
        {{range $pred := .Predicates}}
          {{if ge .Score 10}}
            <span class="highscore">
          {{else if gt .Score 0}}
            <span class="posscore">
          {{else if le .Score -10}}
            <span class="lowscore">
          {{else if lt .Score 0}}
            <span class="negscore">
          {{else}}
            <span class="zeroscore">
          {{end}}
          {{.Name}}
          </span>
        {{end}}
      </span>
    </div>
  {{end}}
  </body>
</html>
`

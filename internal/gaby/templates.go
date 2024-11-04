// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"embed"
	_ "embed"
	"path"

	"github.com/google/safehtml/template"
)

// Embed the templates and static files into the binary.
// We must use the FS form in order to make them trusted with the
// github.com/google/safehtml/template API.

//go:embed tmpl/*.tmpl
var tmplFS embed.FS

//go:embed static/*
var staticFS embed.FS

const (
	// Landing pages
	actionLogTmplFile    = "actionlog.tmpl"
	searchPageTmplFile   = "searchpage.tmpl"
	overviewPageTmplFile = "overviewpage.tmpl"

	// Common template file
	commonTmpl = "common.tmpl"
)

func newTemplate(filename string, funcs template.FuncMap) *template.Template {
	return template.Must(template.New(filename).Funcs(funcs).
		ParseFS(template.TrustedFSFromEmbed(tmplFS),
			path.Join("tmpl", filename),
			path.Join("tmpl", commonTmpl)))
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "github.com/google/safehtml"

// A page is a Gaby web page.
// A type must implement this interface to re-use
// the templates defined in tmpl/common.tmpl.
type page interface {
	// Title returns the title of the webpage.
	Title() string
	// Description returns a plain text description of the webpage.
	Description() string
	// NavID returns an identifier for the webpage as a [safeCSS].
	// NavID must be unique across Gaby's pages.
	NavID() safeCSS
	// Styles returns a list of stylesheets to use for this webpage,
	// as [safeURL]s.
	Styles() []safeURL
}

// Shorthands safehtml types.
type (
	safeCSS = safehtml.StyleSheet
	safeURL = safehtml.TrustedResourceURL
)

// Implementations of [page].

func (actionLogPage) Title() string { return "Oscar Action Log" }
func (overviewPage) Title() string  { return "Oscar Overviews" }
func (searchPage) Title() string    { return "Oscar Search" }

func (actionLogPage) Description() string { return "Browse actions taken by Oscar." }
func (overviewPage) Description() string {
	return "Generate overviews of golang/go issues and their comments, or summarize the relationship between a golang/go issue and its related documents."
}
func (searchPage) Description() string {
	return "Search Oscar's database of GitHub issues, Go documentation, and other documents."
}

func (actionLogPage) NavID() safeCSS { return safehtml.StyleSheetFromConstant("actionlog") }
func (overviewPage) NavID() safeCSS  { return safehtml.StyleSheetFromConstant("overview") }
func (searchPage) NavID() safeCSS    { return safehtml.StyleSheetFromConstant("search") }

var (
	styleCSS     = safehtml.TrustedResourceURLFromConstant("static/style.css")
	searchCSS    = safehtml.TrustedResourceURLFromConstant("static/search.css")
	actionLogCSS = safehtml.TrustedResourceURLFromConstant("static/actionlog.css")
	overviewCSS  = safehtml.TrustedResourceURLFromConstant("static/overview.css")
)

func (actionLogPage) Styles() []safeURL { return []safeURL{styleCSS, actionLogCSS} }
func (overviewPage) Styles() []safeURL  { return []safeURL{styleCSS, searchCSS, overviewCSS} }
func (searchPage) Styles() []safeURL    { return []safeURL{styleCSS, searchCSS} }

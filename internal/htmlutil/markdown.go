// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package htmlutil

import (
	"github.com/google/safehtml"
	"github.com/google/safehtml/uncheckedconversions"
	"rsc.io/markdown"
)

// MarkdownToHTML converts trusted markdown text to HTML.
// For untrusted markdown, use [MarkdownToSafeHTML] instead.
func MarkdownToHTML(text string) string {
	p := markdown.Parser{}
	doc := p.Parse(text)
	return markdown.ToHTML(doc)
}

// MarkdownToSafeHTML converts untrusted markdown text to safe HTML.
// It escapes any HTML present in the original markdown document
// before converting the document to HTML.
func MarkdownToSafeHTML(text string) safehtml.HTML {
	escaped := safehtml.HTMLEscaped(text)
	// Note: [markdown.ToHTML] is trusted and does not add script
	// or style tags.
	html := MarkdownToHTML(escaped.String())
	return uncheckedconversions.HTMLFromStringKnownToSatisfyTypeContract(html)
}

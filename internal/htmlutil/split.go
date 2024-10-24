// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package htmlutil provides HTML utilities.
package htmlutil

import (
	"bytes"
	"iter"
	"strings"

	htmlpkg "golang.org/x/net/html"
)

// A Section is an HTML document section,
// which is the text following an HTML heading
// with an anchor ID.
type Section struct {
	Title string // title of heading
	ID    string // anchor ID of heading
	Text  string // text following heading
}

// Split returns an iterator over sections in html.
func Split(html []byte) iter.Seq[*Section] {
	return func(yield func(*Section) bool) {
		doc, err := htmlpkg.Parse(bytes.NewReader(html))
		if err != nil {
			// Unreachable: htmlpkg.Parse can only fail if there is a read error,
			// which there won't be from bytes.NewReader,
			// or if it hits one of the configured limits,
			// but we haven't configured any,
			// so we can assume there won't be an error.
			// (There is no such thing as "bad" HTML 5.)
			panic("htmlutil: internal error: HTML 5 parse failed: " + err.Error())
		}
		walkDoc(doc, yield)
	}
}

// walkDoc walks the HTML document rooted at n looking for headings.
// When it finds one, it calls walkHeading to handle that section
// of the document.
func walkDoc(n *htmlpkg.Node, yield func(*Section) bool) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if heading(c) >= 1 {
			// Found headings.
			return walkHeadings(c, yield)
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if !walkDoc(c, yield) {
			return false
		}
	}
	return true
}

// walkHeading walks the headings starting at n
// and following through n's siblings, treating each
// as the potential start of a section.
// It yields each section that it encounters.
func walkHeadings(n *htmlpkg.Node, yield func(*Section) bool) bool {
	// Accumulated text for section, which ends at next heading.
	var titles [6]string
	var text strings.Builder
	var lastID string

	// flush flushes the accumulated text.
	flush := func(level int, id string) bool {
		if level > 1 {
			// Construct a title that gives the sequence of heading titles (h1 title > h2 title > ...).
			title := titles[0]
			for _, s := range titles[1:] {
				if s != "" {
					title += " > " + s
				}
			}

			// Emit the section.
			txt := strings.TrimSpace(text.String())
			if txt != "" && lastID != "" {
				if !yield(&Section{Title: title, ID: lastID, Text: txt}) {
					return false
				}
			}
		}

		// Clear headings below the one we are adding now
		// and reset the accumulated text.
		clear(titles[level-1:])
		text.Reset()
		lastID = id
		return true
	}

	// Walk siblings looking for headings, and emit text between them.
	for c := n; c != nil; c = c.NextSibling {
		if i := heading(c); i >= 1 {
			if !flush(i, findAttr(c, "id")) {
				return false
			}
			var buf strings.Builder
			addText(&buf, c)
			titles[i-1] = strings.ReplaceAll(buf.String(), "\n", " ")
			continue
		}
		addText(&text, c)
	}

	// Pretend there's a final very deep heading to flush the last section.
	return flush(len(titles)+1, "zzz")
}

// heading reports the heading level of the node n.
// If n is not a heading, it returns 0.
func heading(n *htmlpkg.Node) int {
	if n.Type == htmlpkg.ElementNode {
		if len(n.Data) == 2 && n.Data[0] == 'h' && '1' <= n.Data[1] && n.Data[1] <= '6' {
			return int(n.Data[1] - '0')
		}
	}
	return 0
}

// addText adds the text from n to buf.
func addText(buf *strings.Builder, n *htmlpkg.Node) {
	if n.Type == htmlpkg.TextNode {
		buf.WriteString(n.Data)
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		addText(buf, c)
	}
}

// findAttr returns the value for n's attribute with the given name.
func findAttr(n *htmlpkg.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package htmlutil

import "testing"

func TestMarkdownToHTML(t *testing.T) {
	text :=
		`This is some **markdown**.

# Heading

Some text.

## Subheading

A list:

* Item 1
* Item 2
`
	got := MarkdownToHTML(text)
	want := `<p>This is some <strong>markdown</strong>.</p>
<h1>Heading</h1>
<p>Some text.</p>
<h2>Subheading</h2>
<p>A list:</p>
<ul>
<li>Item 1</li>
<li>Item 2</li>
</ul>
`
	if got != want {
		t.Errorf("MarkdownToHTML(%q) = %q, want %q\n", text, got, want)
	}
}

func TestMarkdownToSafeHTML(t *testing.T) {
	text := `This is markdown containing unsafe elements.

# Heading

<script>Oh no!</script>
`
	got := MarkdownToSafeHTML(text).String()
	want := `<p>This is markdown containing unsafe elements.</p>
<h1>Heading</h1>
<p>&lt;script&gt;Oh no!&lt;/script&gt;</p>
`
	if got != want {
		t.Errorf("MarkdownToSafeHTML(%q) = %q, want %q\n", text, got, want)
	}
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package docs

import (
	"slices"
	"strings"
	"testing"

	"golang.org/x/oscar/internal/storage"
)

func TestCorpus(t *testing.T) {
	db := storage.MemDB()

	corpus := New(db)
	corpus.Add("id1", "Title1", "text1")
	corpus.Add("id3", "Title3", "text3")
	corpus.Add("id2", "Title2", "text2")

	extra := make(map[string]string)
	var ids []string
	do := func(d *Doc) {
		t.Helper()
		if !strings.HasPrefix(d.ID, "id") {
			t.Fatalf("invalid prefix %q", d.ID)
		}
		n := d.ID[len("id"):]
		title := "Title" + n + extra[d.ID]
		text := "text" + n + extra[d.ID]
		if d.Title != title || d.Text != text {
			t.Fatalf("Doc id=%s has Title=%q, Text=%q, want %q, %q", d.ID, d.Title, d.Text, title, text)
		}
		ids = append(ids, d.ID)
	}

	// Basic iteration.
	for d := range corpus.Docs("") {
		do(d)
	}
	want := []string{"id1", "id2", "id3"}
	if !slices.Equal(ids, want) {
		t.Errorf("Docs() = %v, want %v", ids, want)
	}

	// Break during iteration.
	ids = nil
	for d := range corpus.Docs("") {
		do(d)
		if d.ID == "id2" {
			break
		}
	}
	want = []string{"id1", "id2"}
	if !slices.Equal(ids, want) {
		t.Errorf("Docs with break = %v, want %v", ids, want)
	}

	// DocsAfter iteration uses insert order.
	var last *Doc
	ids = nil
	for d := range corpus.DocsAfter(0, "") {
		do(d)
		last = d
	}
	want = []string{"id1", "id3", "id2"}
	if !slices.Equal(ids, want) {
		t.Errorf("Docs() = %v, want %v", ids, want)
	}

	// DocsAfter incremental iteration.
	corpus.Add("id4", "Title4", "text4")
	extra["id2"] = "X"
	corpus.Add("id2", "Title2X", "text2X") // edits existing text
	corpus.Add("id3", "Title3", "text3")   // no-op, ignored
	ids = nil
	for d := range corpus.DocsAfter(last.DBTime, "") {
		do(d)
	}
	want = []string{"id4", "id2"}
	if !slices.Equal(ids, want) {
		t.Errorf("DocsAfter(last.DBTime=%d) = %v, want %v", last.DBTime, ids, want)
	}

	// DocsAfter with break.
	ids = nil
	for d := range corpus.DocsAfter(last.DBTime, "") {
		do(d)
		break
	}
	want = []string{"id4"}
	if !slices.Equal(ids, want) {
		t.Errorf("DocsAfter(last.DBTime=%d) with break = %v, want %v", last.DBTime, ids, want)
	}

	// Docs with prefix.
	corpus.Add("id11", "Title11", "text11")
	ids = nil
	for d := range corpus.Docs("id1") {
		do(d)
	}
	want = []string{"id1", "id11"}
	if !slices.Equal(ids, want) {
		t.Errorf("Docs(id1) = %v, want %v", ids, want)
	}

	// DocsAfter with prefix.
	ids = nil
	for d := range corpus.DocsAfter(0, "id1") {
		do(d)
	}
	want = []string{"id1", "id11"}
	if !slices.Equal(ids, want) {
		t.Errorf("DocsAfter(0, id1) = %v, want %v", ids, want)
	}
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package docs implements a corpus of text documents identified by document IDs.
// It allows retrieving the documents by ID as well as retrieving documents that are
// new since a previous scan.
package docs

import (
	"iter"
	"strings"

	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

const docsKind = "docs.Doc"

// This package stores the following key schemas in the database:
//
//	["docs.Doc", URL] => [DBTime, Title, Text]
//	["docs.DocByTime", DBTime, URL] => []
//
// DocByTime is an index of Docs by DBTime, which is the time when the
// record was added to the database. Code that processes new docs can
// record which DBTime it has most recently processed and then scan forward in
// the index to learn about new docs.

// A Corpus is the collection of documents stored in a database.
type Corpus struct {
	db storage.DB
}

// New returns a new Corpus representing the documents stored in db.
func New(db storage.DB) *Corpus {
	return &Corpus{db}
}

// A Doc is a single document in the Corpus.
type Doc struct {
	DBTime timed.DBTime // DBTime when Doc was written
	ID     string       // document identifier (such as a URL)
	Title  string       // title of document
	Text   string       // text of document
}

// decodeDoc decodes the document in the timed key-value pair.
// It calls c.db.Panic if the key-value pair is malformed.
func (c *Corpus) decodeDoc(t *timed.Entry) *Doc {
	d := new(Doc)
	d.DBTime = t.ModTime
	if err := ordered.Decode(t.Key, &d.ID); err != nil {
		// unreachable unless db corruption
		c.db.Panic("docs decode", "key", storage.Fmt(t.Key), "err", err)
	}
	if err := ordered.Decode(t.Val, &d.Title, &d.Text); err != nil {
		// unreachable unless db corruption
		c.db.Panic("docs decode", "key", storage.Fmt(t.Key), "val", storage.Fmt(t.Val), "err", err)
	}
	return d
}

// Get returns the document with the given id.
// It returns nil, false if no document is found.
// It returns d, true otherwise.
func (c *Corpus) Get(id string) (doc *Doc, ok bool) {
	t, ok := timed.Get(c.db, docsKind, ordered.Encode(id))
	if !ok {
		return nil, false
	}
	return c.decodeDoc(t), true
}

// Add adds a document with the given id, title, and text.
// If the document already exists in the corpus with the same title and text,
// Add is an no-op.
// Otherwise, if the document already exists in the corpus, it is replaced.
func (c *Corpus) Add(id, title, text string) {
	old, ok := c.Get(id)
	if ok && old.Title == title && old.Text == text {
		return
	}
	b := c.db.Batch()
	timed.Set(c.db, b, docsKind, ordered.Encode(id), ordered.Encode(title, text))
	b.Apply()
}

// Docs returns an iterator over all documents in the corpus
// with IDs starting with a given prefix.
// The documents are ordered by ID.
func (c *Corpus) Docs(prefix string) iter.Seq[*Doc] {
	return func(yield func(*Doc) bool) {
		for t := range timed.Scan(c.db, docsKind, ordered.Encode(prefix), ordered.Encode(prefix+"\xff")) {
			if !yield(c.decodeDoc(t)) {
				return
			}
		}
	}
}

// DocsAfter returns an iterator over all documents with DBTime
// greater than dbtime and with IDs starting with the prefix.
// The documents are ordered by DBTime.
func (c *Corpus) DocsAfter(dbtime timed.DBTime, prefix string) iter.Seq[*Doc] {
	filter := func(key []byte) bool {
		if prefix == "" {
			return true
		}
		var id string
		if err := ordered.Decode(key, &id); err != nil {
			// unreachable unless db corruption
			c.db.Panic("docs scan decode", "key", storage.Fmt(key), "err", err)
		}
		return strings.HasPrefix(id, prefix)
	}
	return func(yield func(*Doc) bool) {
		for t := range timed.ScanAfter(c.db, docsKind, dbtime, filter) {
			if !yield(c.decodeDoc(t)) {
				return
			}
		}
	}
}

// DocWatcher returns a new [storage.Watcher] with the given name.
// It picks up where any previous Watcher of the same name left off.
func (c *Corpus) DocWatcher(name string) *timed.Watcher[*Doc] {
	return timed.NewWatcher(c.db, name, docsKind, c.decodeDoc)
}

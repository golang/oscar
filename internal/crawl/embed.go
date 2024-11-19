// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package crawl

import (
	"iter"

	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/htmlutil"
	"golang.org/x/oscar/internal/storage/timed"
)

var _ docs.Source[*Page] = (*Crawler)(nil)

const DocWatcherID = "crawldocs"

// DocWatcher returns the page watcher with name "crawldocs".
// Implements [docs.Source.DocWatcher].
func (cr *Crawler) DocWatcher() *timed.Watcher[*Page] {
	return cr.PageWatcher(DocWatcherID)
}

// ToDocs converts a crawled page to a list of embeddable documents,
// split into sections using [htmlutil.Split].
//
// Implements [docs.Source.ToDocs].
func (*Crawler) ToDocs(p *Page) (iter.Seq[*docs.Doc], bool) {
	return func(yield func(*docs.Doc) bool) {
		// TODO(rsc): We should probably delete the existing docs
		// starting with p.URL# before embedding them.
		for s := range htmlutil.Split(p.HTML) {
			d := &docs.Doc{
				ID:    p.URL + "#" + s.ID,
				Title: s.Title,
				Text:  s.Text,
			}
			if !yield(d) {
				return
			}
		}
	}, true
}

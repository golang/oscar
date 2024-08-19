// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package crawldocs

import (
	"context"
	"log/slog"

	"golang.org/x/oscar/internal/crawl"
	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/storage/timed"
)

// Sync reads new HTML pages from cr, splits them into sections using [Split],
// and adds each page section's text to the corpus dc.
//
// Sync uses [crawl.Crawler.PageWatcher] with the name "crawldocs"
// to save its position across multiple calls.
//
// Sync logs status and unexpected problems to lg.
//
// Sync makes no use of its context.
func Sync(ctx context.Context, lg *slog.Logger, dc *docs.Corpus, cr *crawl.Crawler) {
	w := cr.PageWatcher("crawldocs")
	for p := range w.Recent() {
		lg.Debug("crawldocs sync", "page", p.URL, "dbtime", p.DBTime)
		// TODO(rsc): We should probably delete the existing docs
		// starting with p.URL#.
		for s := range Split(p.HTML) {
			dc.Add(p.URL+"#"+s.ID, s.Title, s.Text)
		}
		w.MarkOld(p.DBTime)
	}
}

// Restart restarts the "crawldocs" page watcher,
// so that a future call to [Sync] will reprocess all documents.
// Calling [Restart] may be necessary after changing [Split],
// to reprocess those pages.
//
// Restart makes no use of its context.
func Restart(ctx context.Context, lg *slog.Logger, cr *crawl.Crawler) {
	cr.PageWatcher("crawldocs").Restart()
}

// Latest returns the latest known DBTime marked old by the crawler's Watcher.
func Latest(cr *crawl.Crawler) timed.DBTime {
	return cr.PageWatcher("crawldocs").Latest()
}

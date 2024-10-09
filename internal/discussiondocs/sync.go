// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package discussiondocs implements converting GitHub discussions into text docs
// for [golang.org/x/oscar/internal/docs].
package discussiondocs

import (
	"context"
	"log/slog"

	"golang.org/x/oscar/internal/discussion"
	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/storage/timed"
)

// Sync writes to dc docs corresponding to each discussion in gh that is
// new since the last call to Sync.
//
// If a discussion is edited on GitHub, it will appear new in gh and
// the new text will be written to dc, replacing the old issue text.
// Only the discussion body is saved as a document.
//
// The document ID for each discussion is its GitHub URL: "https://github.com/<org>/<repo>/discussions/<n>".
func Sync(ctx context.Context, lg *slog.Logger, dc *docs.Corpus, gh *discussion.Client) error {
	w := gh.EventWatcher(watcherID)
	for e := range w.Recent() {
		if e.API != discussion.DiscussionAPI {
			continue
		}
		lg.Debug("discussiondocs sync", "discussion", e.Discussion, "dbtime", e.DBTime)
		d := e.Typed.(*discussion.Discussion)
		title := cleanTitle(d.Title)
		text := cleanBody(d.Body)
		dc.Add(d.URL, title, text)
		w.MarkOld(e.DBTime)
	}
	return nil
}

const watcherID = "discussiondocs"

// Restart causes the next call to [Sync] to behave as if
// it has never sync'ed any issues before.
// The result is that all issues will be reconverted to doc form
// and re-added.
// Docs that have not changed since the last addition to the corpus
// will appear unmodified; others will be marked new in the corpus.
func Restart(lg *slog.Logger, gh *discussion.Client) {
	gh.EventWatcher(watcherID).Restart()
}

// Latest returns the latest known DBTime marked old by the client's Watcher.
func Latest(gh *discussion.Client) timed.DBTime {
	return gh.EventWatcher(watcherID).Latest()
}

// cleanTitle should clean the title for indexing.
// For now we assume the LLM is good enough at Markdown not to bother.
func cleanTitle(title string) string {
	// TODO
	return title
}

// cleanBody should clean the body for indexing.
// For now we assume the LLM is good enough at Markdown not to bother.
// In the future we may want to make various changes like inlining
// the programs associated with playground URLs,
// and we may also want to remove any HTML tags from the Markdown.
func cleanBody(body string) string {
	// TODO
	return body
}

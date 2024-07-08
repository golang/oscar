// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package githubdocs implements converting GitHub issues into text docs
// for [golang.org/x/oscar/internal/docs].
package githubdocs

import (
	"fmt"
	"log/slog"

	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/github"
)

// Sync writes to dc docs corresponding to each issue in gh that is
// new since the last call to Sync.
//
// If an issue is edited on GitHub, it will appear new in gh and
// the new text will be written to dc, replacing the old issue text.
// Only the issue body (what looks like the top comment in the UI)
// is saved as a document.
// The document ID for each issue is its GitHub URL: "https://github.com/<org>/<repo>/issues/<n>".
func Sync(lg *slog.Logger, dc *docs.Corpus, gh *github.Client) {
	w := gh.EventWatcher("githubdocs")
	for e := range w.Recent() {
		if e.API != "/issues" {
			continue
		}
		lg.Debug("githubdocs sync", "issue", e.Issue, "dbtime", e.DBTime)
		issue := e.Typed.(*github.Issue)
		title := cleanTitle(issue.Title)
		text := cleanBody(issue.Body)
		dc.Add(fmt.Sprintf("https://github.com/%s/issues/%d", e.Project, e.Issue), title, text)
		w.MarkOld(e.DBTime)
	}
}

// Restart causes the next call to Sync to behave as if
// it has never sync'ed any issues before.
// The result is that all issues will be reconverted to doc form
// and re-added.
// Docs that have not changed since the last addition to the corpus
// will appear unmodified; others will be marked new in the corpus.
func Restart(lg *slog.Logger, gh *github.Client) {
	gh.EventWatcher("githubdocs").Restart()
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

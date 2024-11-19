// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"iter"
	"slices"

	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/storage/timed"
)

var _ docs.Source[*Event] = (*Client)(nil)

const DocWatcherID = "githubdocs"

// DocWatcher returns the event watcher with name "githubdocs".
// Implements [docs.Source.DocWatcher].
func (c *Client) DocWatcher() *timed.Watcher[*Event] {
	return c.EventWatcher(DocWatcherID)
}

// ToDocs converts an event containing an issue to an
// embeddable document.
// It returns (nil, false) if the event is not an issue.
// Implements [docs.Source.ToDocs].
func (*Client) ToDocs(e *Event) (iter.Seq[*docs.Doc], bool) {
	issue, ok := e.Typed.(*Issue)
	if !ok {
		return nil, false
	}
	return slices.Values([]*docs.Doc{
		{
			ID:    issue.DocID(),
			Title: CleanTitle(issue.Title),
			Text:  CleanBody(issue.Body),
		},
	}), true
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package discussion

import (
	"iter"
	"slices"

	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/storage/timed"
)

var _ docs.Source[*Event] = (*Client)(nil)

const DocWatcherID = "discussiondocs"

// DocWatcher returns the page watcher with name "discussiondocs".
// Implements [docs.Source.DocWatcher].
func (c *Client) DocWatcher() *timed.Watcher[*Event] {
	return c.EventWatcher(DocWatcherID)
}

// ToDocs converts an event containing a discussion to
// an embeddable document (wrapped as an iterator).
// It returns (nil, false) if the event is not a discussion.
// Implements [docs.Source.ToDocs].
func (*Client) ToDocs(e *Event) (iter.Seq[*docs.Doc], bool) {
	d, ok := e.Typed.(*Discussion)
	if !ok {
		return nil, false
	}
	return slices.Values([]*docs.Doc{{
		ID:    d.URL,
		Title: github.CleanTitle(d.Title),
		Text:  github.CleanBody(d.Body),
	}}), true
}

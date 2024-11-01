// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package googlegroups

import (
	"encoding/json"
	"iter"
	"slices"

	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/storage/timed"
)

var _ docs.Source[*ConversationEvent] = (*Client)(nil)

const DocWatcherID = "ggroupsrelateddocs"

// DocWatcher returns the change event watcher with name "ggroupsrelateddocs".
// Implements [docs.Source.DocWatcher].
func (c *Client) DocWatcher() *timed.Watcher[*ConversationEvent] {
	return c.ConversationWatcher(DocWatcherID)
}

// LastWritten implements [docs.Entry.LastWritten].
func (ce *ConversationEvent) LastWritten() timed.DBTime {
	return ce.DBTime
}

// ToDocs converts a ConversationEvent to an embeddable document (wrapped
// as an iterator).
//
// This document consists of the HTML for the first message of a
// Google Group conversation.
//
//	https://groups.google.com/g/<group>/c/<conversation>
//
// ToDocs returns (nil, false) if any of the necessary data cannot be found
// in the client's db.
//
// Implements [docs.Source.ToDocs].
func (c *Client) ToDocs(ce *ConversationEvent) (iter.Seq[*docs.Doc], bool) {
	key := o(conversationKind, ce.Group, ce.URL)
	val, ok := c.db.Get(key)
	if !ok {
		c.slog.Error("ggroups.ToDocs cannot find conversation", "URL", ce.URL)
		return nil, false
	}
	var conv Conversation
	if err := json.Unmarshal(val, &conv); err != nil {
		c.slog.Error("ggroups.ToDocs conversation decode failure", "URL", ce.URL, "err", err)
		return nil, false
	}

	title := conv.Title
	if title == "" {
		title = conv.URL // for sanity
	}
	// Embed only the first conversation message.
	return slices.Values([]*docs.Doc{{
		ID:    conv.URL,
		Title: title,
		Text:  conv.Messages[0],
	}}), true
}

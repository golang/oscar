// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package googlegroups

import (
	"encoding/json"
	"iter"
	"time"

	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

// Conversations returns an iterator over the group conversations.
// The first iterator value is the conversation URL.
// The second iterator value is a function that can be called to
// return information about the conversation, as with [storage.DB.Scan].
func (c *Client) Conversations(group string) iter.Seq2[string, func() *Conversation] {
	return func(yield func(string, func() *Conversation) bool) {
		for key, fn := range c.db.Scan(o(conversationKind, group), o(conversationKind, group, ordered.Inf)) {
			var changeURL string
			if err := ordered.Decode(key, nil, nil, &changeURL); err != nil {
				c.db.Panic("ggroup client conversation decode", "key", storage.Fmt(key), "err", err)
			}
			cfn := func() *Conversation {
				var conv Conversation
				if err := json.Unmarshal(fn(), &conv); err != nil {
					c.db.Panic("ggroup client conversation unmarshal", "key", storage.Fmt(key), "err", err)
				}
				return &conv
			}
			if !yield(changeURL, cfn) {
				return
			}
		}
	}
}

// timeStampLayout is the timestamp format used by Google Groups search.
const timeStampLayout = time.DateOnly

// Conversation is a Google Group conversation
// represented by HTML from Google Groups web page.
type Conversation struct {
	// Group name.
	Group string
	// Title of the conversation.
	Title string
	// URL points to the Google Groups web
	// page of the conversation. The page
	// contains conversation messages.
	URL string
	// Messages are raw html data that contain
	// individual conversation messages obtained
	// from URL.
	Messages []string

	updated   string // for testing
	interrupt bool   // for testing
}

// A ConversationEvent is a Google Groups conversation
// change event returned by Conversationatcher.
type ConversationEvent struct {
	DBTime timed.DBTime // time of the change
	Group  string       // group name
	URL    string       // group URL
}

// ConversationWatcher returns a new [timed.Watcher] with the given name.
// It picks up where any previous Watcher of the same name left off.
func (c *Client) ConversationWatcher(name string) *timed.Watcher[ConversationEvent] {
	return timed.NewWatcher(c.slog, c.db, name, conversationKind, c.decodeConversationEvent)
}

// decodeConversationEvent decodes a conversationKind [timed.Entry] into
// a conversation event.
func (c *Client) decodeConversationEvent(t *timed.Entry) ConversationEvent {
	ce := ConversationEvent{
		DBTime: t.ModTime,
	}
	if err := ordered.Decode(t.Key, &ce.Group, &ce.URL, nil); err != nil {
		c.db.Panic("ggroups conversation event decode", "key", storage.Fmt(t.Key), "err", err)
	}
	return ce
}

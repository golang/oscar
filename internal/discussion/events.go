// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package discussion

import (
	"encoding/json"
	"iter"
	"math"
	"time"

	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

// NOTE: the code in this file is very similar to the internal/github
// package and can probably be merged once we're confident the discussion
// sync is working.
// For now, they are separate to avoid accidental interaction between
// the two packages's database entries.

// EventWatcher returns a new [timed.Watcher] with the given name.
// It picks up where any previous Watcher of the same name left off.
func (c *Client) EventWatcher(name string) *timed.Watcher[*Event] {
	return timed.NewWatcher(c.slog, c.db, name, eventKind, c.decodeEvent)
}

// An Event is a single GitHub discussion event stored in the database.
type Event struct {
	DBTime     timed.DBTime // when event was last written
	Project    string       // project (e.g. "golang/go")
	Discussion int64        // discussion number
	API        string       // the event kind ("API" for consistency with the [github.Event.API] field)
	ID         int64        // ID of event; each API has a different ID space. (Project, Discussion, API, ID) is assumed unique
	JSON       []byte       // JSON for the event data
	Typed      any          // Typed unmarshaling of the event data, of type [*Discussion], [*Comment]
	Updated    time.Time    // when the event was last updated (according to GitHub)
}

// The recognized event kinds.
// The events are fetched from the GrapQL API, which
// uses queries instead of API endpoints, so these "endpoints"
// are merely for identification purposes.
// We use the term API for consistency with the [github.Event.API] field.
const (
	DiscussionAPI string = "/discussions"
	CommentAPI    string = "/discussions/comments" // both comments and replies
)

// decodeEvent decodes the key, val pair into an Event.
// It calls c.db.Panic for malformed data.
func (c *Client) decodeEvent(t *timed.Entry) *Event {
	var e Event
	e.DBTime = t.ModTime
	if err := ordered.Decode(t.Key, &e.Project, &e.Discussion, &e.API, &e.ID); err != nil {
		c.db.Panic("discussion event decode", "key", storage.Fmt(t.Key), "err", err)
	}

	var js ordered.Raw
	if err := ordered.Decode(t.Val, &js); err != nil {
		c.db.Panic("discussion event val decode", "key", storage.Fmt(t.Key), "val", storage.Fmt(t.Val), "err", err)
	}
	e.JSON = js
	switch e.API {
	default:
		c.db.Panic("discussion event invalid kind", "kind", e.API)
	case DiscussionAPI:
		e.Typed = new(Discussion)
	case CommentAPI:
		e.Typed = new(Comment)
	}
	if err := json.Unmarshal(js, e.Typed); err != nil {
		c.db.Panic("discussion event json", "js", string(js), "err", err)
	}
	return &e
}

// Events returns an iterator over discussion events for the given project,
// limited to discussions in the range discMin ≤ discussion ≤ discMax.
// If discMax < 0, there is no upper limit.
// The events are iterated over in (Project, Discussion, Kind, ID) order,
// so "/discussions" events come first, then "/discussions/comments"
// events.
// Within an event kind, the events are ordered by increasing ID,
// which corresponds to increasing event time on GitHub.
func (c *Client) Events(project string, discMin, discMax int64) iter.Seq[*Event] {
	return func(yield func(*Event) bool) {
		start := o(project, discMin)
		if discMax < 0 {
			discMax = math.MaxInt64
		}
		end := o(project, discMax, ordered.Inf)
		for t := range timed.Scan(c.db, eventKind, start, end) {
			if !yield(c.decodeEvent(t)) {
				return
			}
		}
	}
}

// EventsAfter returns an iterator over discussion events in the given project after DBTime t,
// which should be e.DBTime from the most recent processed event.
// The events are iterated over in DBTime order, so the DBTime of the last
// successfully processed event can be used in a future call to EventsAfter.
// If project is the empty string, then events from all projects are returned.
func (c *Client) EventsAfter(t timed.DBTime, project string) iter.Seq[*Event] {
	filter := func(key []byte) bool {
		if project == "" {
			return true
		}
		var p string
		if _, err := ordered.DecodePrefix(key, &p); err != nil {
			c.db.Panic("discussion.EventsAfter decode", "key", storage.Fmt(key), "err", err)
		}
		return p == project
	}

	return func(yield func(*Event) bool) {
		for e := range timed.ScanAfter(c.slog, c.db, eventKind, t, filter) {
			if !yield(c.decodeEvent(e)) {
				return
			}
		}
	}
}

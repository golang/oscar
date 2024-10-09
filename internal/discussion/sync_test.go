// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package discussion

import (
	"bytes"
	"context"
	"iter"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"golang.org/x/oscar/internal/testutil"
)

func TestSync(t *testing.T) {
	ctx := context.Background()
	db := storage.MemDB()
	check := testutil.Checker(t)
	c := &Client{
		gql:  testGQLClientFromFile(t, "testdata/scratch.httprr"),
		slog: testutil.Slogger(t),
		db:   db,
	}
	restore := maxItemsPerPage
	maxItemsPerPage = 2
	t.Cleanup(func() { maxItemsPerPage = restore })

	check(c.Add(scratchProject))

	// Initial load.
	check(c.Sync(ctx))

	diffEvents(t,
		"initial",
		collectEvents(c.Events(scratchProject, -1, -1)),
		scratchEvents1)

	w := c.EventWatcher("test1")
	for e := range w.Recent() {
		w.MarkOld(e.DBTime)
	}

	// Incremental update.
	c2 := &Client{
		gql:  testGQLClientFromFile(t, "testdata/scratch2.httprr"),
		slog: testutil.Slogger(t),
		db:   db,
	}
	check(c2.Sync(ctx))

	// Test that EventWatcher sees the updates.
	diffEvents(t,
		"updates",
		collectEventsAfter(t, 0, c.EventWatcher("test1").Recent()),
		scratchNewEvents)

	// Test that without MarkOld, Recent leaves the cursor where it was.
	diffEvents(t,
		"no mark old",
		collectEventsAfter(t, 0, c.EventWatcher("test1").Recent()),
		scratchNewEvents)

	// All the events should be present in order.
	have := collectEvents(c.Events(scratchProject, -1, -1))
	diffEvents(t, "all", have, scratchEvents2)

	// Again with an early break.
	have = have[:0]
	for e := range c.Events(scratchProject, -1, -1) {
		have = append(have, o(e.Project, e.Discussion, e.API, e.ID))
		if len(have) == len(scratchEvents2)/2 {
			break
		}
	}
	diffEvents(t, "all early", have, scratchEvents2[:len(scratchEvents2)/2])

	// Again with a different project.
	for range c.Events("fauxlang/faux", -1, 100) {
		t.Errorf("Events: project filter failed")
	}

	// The EventsAfter list should not have any duplicates, even though
	// the incremental sync revisited some issues.
	have = collectEventsAfter(t, 0, c.EventsAfter(0, ""))
	diffEvents(t, "events after", have, scratchEvents2)

	// Again with an early break.
	have = have[:0]
	for e := range c.EventsAfter(0, "") {
		have = append(have, e.key())
		if len(have) == len(scratchEarlyEvents) {
			break
		}
	}
	diffEvents(t, "events after early", have, scratchEarlyEvents)

	// Again with a different project.
	for range c.EventsAfter(0, "fauxlang/faux") {
		t.Errorf("EventsAfter: project filter failed")
	}
}

func diffEvents(t *testing.T, name string, have, want [][]byte) {
	t.Helper()

	// Format for readability.
	gotS, wantS := make([]string, len(have)), make([]string, len(want))
	for i, key := range have {
		gotS[i] = storage.Fmt(key)
	}
	for i, key := range want {
		wantS[i] = storage.Fmt(key)
	}
	if diff := cmp.Diff(wantS, gotS); diff != "" {
		t.Errorf("%s: mismatch (-want, +got):\n%s", name, diff)
	}
}

func collectEvents(seq iter.Seq[*Event]) [][]byte {
	var keys [][]byte
	for e := range seq {
		keys = append(keys, e.key())
	}
	return keys
}

func collectEventsAfter(t *testing.T, dbtime timed.DBTime, seq iter.Seq[*Event]) [][]byte {
	var keys [][]byte
	for e := range seq {
		if e.DBTime <= dbtime {
			t.Errorf("EventsAfter: DBTime inversion: e.DBTime %d <= last %d", e.DBTime, dbtime)
		}
		dbtime = e.DBTime
		keys = append(keys, e.key())
	}
	slices.SortFunc(keys, bytes.Compare)
	return keys
}

var (
	scratchProject = "tatianab/scratch"
	// The first three events as of testdata/scratch2.httprr, in the order
	// returned by [Client.EventsAfter] (dbtime order).
	scratchEarlyEvents = [][]byte{
		o(scratchProject, 51, DiscussionAPI, 51),
		o(scratchProject, 53, DiscussionAPI, 53),
		o(scratchProject, 52, DiscussionAPI, 52),
	}
	// The events as of testdata/scratch.httprr, in the order
	// returned by [Client.Events]
	scratchEvents1 = [][]byte{
		o(scratchProject, 50, DiscussionAPI, 50),
		o(scratchProject, 50, CommentAPI, 10870119),
		o(scratchProject, 50, CommentAPI, 10870121),
		o(scratchProject, 50, CommentAPI, 10870125),
		o(scratchProject, 50, CommentAPI, 10870127),
		o(scratchProject, 50, CommentAPI, 10881941),
		o(scratchProject, 50, CommentAPI, 10881945),
		o(scratchProject, 51, DiscussionAPI, 51),
		o(scratchProject, 51, CommentAPI, 10870149),
		o(scratchProject, 51, CommentAPI, 10870153),
		o(scratchProject, 51, CommentAPI, 10870157),
		o(scratchProject, 51, CommentAPI, 10870161),
		o(scratchProject, 51, CommentAPI, 10870165),
		o(scratchProject, 51, CommentAPI, 10870169),
		o(scratchProject, 52, DiscussionAPI, 52),
		o(scratchProject, 52, CommentAPI, 10870178),
		o(scratchProject, 53, DiscussionAPI, 53),
	}
	// The events as of testdata/scratch2.httprr, in the order
	// returned by [Client.Events]
	scratchEvents2 = [][]byte{
		o(scratchProject, 50, DiscussionAPI, 50),
		o(scratchProject, 50, CommentAPI, 10870119),
		o(scratchProject, 50, CommentAPI, 10870121),
		o(scratchProject, 50, CommentAPI, 10870125),
		o(scratchProject, 50, CommentAPI, 10870127),
		o(scratchProject, 50, CommentAPI, 10881941),
		o(scratchProject, 50, CommentAPI, 10881945),
		o(scratchProject, 50, CommentAPI, 10883560), // new
		o(scratchProject, 50, CommentAPI, 10883563), // new
		o(scratchProject, 51, DiscussionAPI, 51),
		o(scratchProject, 51, CommentAPI, 10870149),
		o(scratchProject, 51, CommentAPI, 10870153),
		o(scratchProject, 51, CommentAPI, 10870157),
		o(scratchProject, 51, CommentAPI, 10870161),
		o(scratchProject, 51, CommentAPI, 10870165),
		o(scratchProject, 51, CommentAPI, 10870169),
		o(scratchProject, 52, DiscussionAPI, 52),
		o(scratchProject, 52, CommentAPI, 10870178),
		o(scratchProject, 53, DiscussionAPI, 53),
		o(scratchProject, 54, DiscussionAPI, 54), // new
	}
	// Diff between testdata/scratch.httprr and testdata/scratch2.httprr.
	scratchNewEvents = [][]byte{
		o(scratchProject, 50, DiscussionAPI, 50),    // update
		o(scratchProject, 50, CommentAPI, 10883560), // new
		o(scratchProject, 50, CommentAPI, 10883563), // new
		o(scratchProject, 54, DiscussionAPI, 54),    // new
	}
)

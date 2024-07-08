// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"bytes"
	"errors"
	"iter"
	"net/http"
	"slices"
	"testing"

	"golang.org/x/oscar/internal/httprr"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"golang.org/x/oscar/internal/testutil"
)

func TestMarkdown(t *testing.T) {
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()

	// Initial load.
	rr, err := httprr.Open("testdata/markdown.httprr", http.DefaultTransport)
	check(err)
	rr.Scrub(Scrub)
	sdb := secret.Empty()
	if rr.Recording() {
		sdb = secret.Netrc()
	}
	c := New(lg, db, sdb, rr.Client())
	check(c.Add("rsc/markdown"))
	check(c.Sync())

	w := c.EventWatcher("test1")
	for e := range w.Recent() {
		w.MarkOld(e.DBTime)
	}

	// Incremental update.
	rr, err = httprr.Open("testdata/markdown2.httprr", http.DefaultTransport)
	check(err)
	rr.Scrub(Scrub)
	sdb = secret.Empty()
	if rr.Recording() {
		sdb = secret.Netrc()
	}
	c = New(lg, db, sdb, rr.Client())
	check(c.Sync())

	// Test that EventWatcher sees the updates.
	diffEvents(t,
		collectEventsAfter(t, 0, c.EventWatcher("test1").Recent()),
		markdownNewEvents)

	// Test that without MarkOld, Recent leaves the cursor where it was.
	diffEvents(t,
		collectEventsAfter(t, 0, c.EventWatcher("test1").Recent()),
		markdownNewEvents)

	// Incremental update.
	rr, err = httprr.Open("testdata/markdown3.httprr", http.DefaultTransport)
	check(err)
	rr.Scrub(Scrub)
	sdb = secret.Empty()
	if rr.Recording() {
		sdb = secret.Netrc()
	}
	c = New(lg, db, sdb, rr.Client())
	check(c.Sync())

	testMarkdownEvents(t, c)
}

func TestMarkdownIncrementalSync(t *testing.T) {
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()

	// Initial load.
	rr, err := httprr.Open("testdata/markdowninc.httprr", http.DefaultTransport)
	check(err)
	rr.Scrub(Scrub)
	sdb := secret.Empty()
	if rr.Recording() {
		sdb = secret.Netrc()
	}
	c := New(lg, db, sdb, rr.Client())
	check(c.Add("rsc/markdown"))

	testFullSyncStop = errors.New("stop for testing")
	defer func() {
		testFullSyncStop = nil
	}()
	for {
		err := c.Sync()
		if err == nil {
			break
		}
		if !errors.Is(err, testFullSyncStop) {
			t.Fatal(err)
		}
	}

	testMarkdownEvents(t, c)
}

func testMarkdownEvents(t *testing.T, c *Client) {
	// All the events should be present in order.
	have := collectEvents(c.Events("rsc/markdown", -1, -1))
	diffEvents(t, have, markdownEvents)

	// Again with an early break.
	have = have[:0]
	for e := range c.Events("rsc/markdown", -1, 100) {
		have = append(have, o(e.Project, e.Issue, e.API, e.ID))
		if len(have) == len(markdownEvents)/2 {
			break
		}
	}
	diffEvents(t, have, markdownEvents[:len(markdownEvents)/2])

	// Again with a different project.
	for _ = range c.Events("fauxlang/faux", -1, 100) {
		t.Errorf("EventsAfter: project filter failed")
	}

	// The EventsByTime list should not have any duplicates, even though
	// the incremental sync revisited some issues.
	have = collectEventsAfter(t, 0, c.EventsAfter(0, ""))
	diffEvents(t, have, markdownEvents)

	// Again with an early break.
	have = have[:0]
	for e := range c.EventsAfter(0, "") {
		have = append(have, o(e.Project, e.Issue, e.API, e.ID))
		if len(have) == len(markdownEarlyEvents) {
			break
		}
	}
	diffEvents(t, have, markdownEarlyEvents)

	// Again with a different project.
	for _ = range c.EventsAfter(0, "fauxlang/faux") {
		t.Errorf("EventsAfter: project filter failed")
	}
}

func diffEvents(t *testing.T, have, want [][]byte) {
	t.Helper()
	for _, key := range have {
		for len(want) > 0 && bytes.Compare(want[0], key) < 0 {
			t.Errorf("Events: missing %s", storage.Fmt(want[0]))
			want = want[1:]
		}
		if len(want) > 0 && bytes.Equal(key, want[0]) {
			want = want[1:]
			continue
		}
		t.Errorf("Events: unexpected %s", storage.Fmt(key))
	}
	for len(want) > 0 {
		t.Errorf("Events: missing %s", storage.Fmt(want[0]))
		want = want[1:]
	}
}

func collectEvents(seq iter.Seq[*Event]) [][]byte {
	var keys [][]byte
	for e := range seq {
		keys = append(keys, o(e.Project, e.Issue, e.API, e.ID))
	}
	return keys
}

func collectEventsAfter(t *testing.T, dbtime timed.DBTime, seq iter.Seq[*Event]) [][]byte {
	var keys [][]byte
	for e := range seq {
		if e.DBTime <= dbtime {
			// TODO(rsc): t.Helper probably doesn't apply here but should.
			t.Errorf("EventsSince: DBTime inversion: e.DBTime %d <= last %d", e.DBTime, dbtime)
		}
		dbtime = e.DBTime
		keys = append(keys, o(e.Project, e.Issue, e.API, e.ID))
	}
	slices.SortFunc(keys, bytes.Compare)
	return keys
}

func TestIvy(t *testing.T) {
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	rr, err := httprr.Open("testdata/ivy.httprr", http.DefaultTransport)
	check(err)
	rr.Scrub(Scrub)
	sdb := secret.Empty()
	if rr.Recording() {
		sdb = secret.Netrc()
	}
	c := New(lg, db, sdb, rr.Client())
	check(c.Add("robpike/ivy"))
	check(c.Sync())
}

func TestOmap(t *testing.T) {
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	rr, err := httprr.Open("testdata/omap.httprr", http.DefaultTransport)
	check(err)
	rr.Scrub(Scrub)
	sdb := secret.Empty()
	if rr.Recording() {
		sdb = secret.Netrc()
	}
	c := New(lg, db, sdb, rr.Client())
	check(c.Add("rsc/omap"))
	check(c.Sync())
}

var markdownEarlyEvents = [][]byte{
	o("rsc/markdown", 3, "/issues", 2038510799),
	o("rsc/markdown", 2, "/issues", 2038502414),
	o("rsc/markdown", 4, "/issues", 2038521730),
	o("rsc/markdown", 1, "/issues", 2038380363),
	o("rsc/markdown", 6, "/issues", 2038573328),
}

var markdownNewEvents = [][]byte{
	o("rsc/markdown", 16, "/issues", 2189605425),
	o("rsc/markdown", 16, "/issues/comments", 2146194902),
	o("rsc/markdown", 16, "/issues/events", 13027435265),
	o("rsc/markdown", 17, "/issues", 2189605911),
	o("rsc/markdown", 17, "/issues/comments", 2146194573),
	o("rsc/markdown", 17, "/issues/comments", 2146421109),
	o("rsc/markdown", 17, "/issues/events", 13027432818),
	o("rsc/markdown", 17, "/issues/events", 13028910699),
	o("rsc/markdown", 17, "/issues/events", 13028910702),
	o("rsc/markdown", 18, "/issues", 2276848742),
	o("rsc/markdown", 18, "/issues/comments", 2097019306),
	o("rsc/markdown", 18, "/issues/comments", 2146475274),
	o("rsc/markdown", 18, "/issues/events", 13027289256),
	o("rsc/markdown", 18, "/issues/events", 13027289270),
	o("rsc/markdown", 18, "/issues/events", 13027289466),
	o("rsc/markdown", 19, "/issues", 2308816936),
	o("rsc/markdown", 19, "/issues/comments", 2146197528),
}

var markdownEvents = [][]byte{
	o("rsc/markdown", 1, "/issues", 2038380363),
	o("rsc/markdown", 1, "/issues/events", 11230676272),
	o("rsc/markdown", 2, "/issues", 2038502414),
	o("rsc/markdown", 2, "/issues/events", 11230676151),
	o("rsc/markdown", 3, "/issues", 2038510799),
	o("rsc/markdown", 3, "/issues/comments", 1852808662),
	o("rsc/markdown", 3, "/issues/events", 11228615168),
	o("rsc/markdown", 3, "/issues/events", 11228628324),
	o("rsc/markdown", 3, "/issues/events", 11230676181),
	o("rsc/markdown", 4, "/issues", 2038521730),
	o("rsc/markdown", 4, "/issues/events", 11230676170),
	o("rsc/markdown", 5, "/issues", 2038530418),
	o("rsc/markdown", 5, "/issues/comments", 1852919031),
	o("rsc/markdown", 5, "/issues/comments", 1854409176),
	o("rsc/markdown", 5, "/issues/events", 11230676200),
	o("rsc/markdown", 5, "/issues/events", 11239005964),
	o("rsc/markdown", 6, "/issues", 2038573328),
	o("rsc/markdown", 6, "/issues/events", 11230676238),
	o("rsc/markdown", 7, "/issues", 2040197050),
	o("rsc/markdown", 7, "/issues/events", 11241620840),
	o("rsc/markdown", 8, "/issues", 2040277497),
	o("rsc/markdown", 8, "/issues/comments", 1854835554),
	o("rsc/markdown", 8, "/issues/comments", 1854837832),
	o("rsc/markdown", 8, "/issues/comments", 1856133592),
	o("rsc/markdown", 8, "/issues/comments", 1856151124),
	o("rsc/markdown", 8, "/issues/events", 11250194227),
	o("rsc/markdown", 9, "/issues", 2040303458),
	o("rsc/markdown", 9, "/issues/events", 11241620809),
	o("rsc/markdown", 10, "/issues", 2076625629),
	o("rsc/markdown", 10, "/issues/comments", 1894927765),
	o("rsc/markdown", 10, "/issues/events", 11456466988),
	o("rsc/markdown", 10, "/issues/events", 11506360992),
	o("rsc/markdown", 11, "/issues", 2076798270),
	o("rsc/markdown", 11, "/issues/comments", 1894929190),
	o("rsc/markdown", 11, "/issues/events", 11506369300),
	o("rsc/markdown", 12, "/issues", 2137605063),
	o("rsc/markdown", 12, "/issues/events", 11822212932),
	o("rsc/markdown", 12, "/issues/events", 11942808811),
	o("rsc/markdown", 12, "/issues/events", 11942812866),
	o("rsc/markdown", 12, "/issues/events", 12028957331),
	o("rsc/markdown", 12, "/issues/events", 12028957356),
	o("rsc/markdown", 12, "/issues/events", 12028957676),
	o("rsc/markdown", 13, "/issues", 2182527101),
	o("rsc/markdown", 13, "/issues/events", 12122378461),
	o("rsc/markdown", 14, "/issues", 2182534654),
	o("rsc/markdown", 14, "/issues/events", 12122340938),
	o("rsc/markdown", 14, "/issues/events", 12122495521),
	o("rsc/markdown", 14, "/issues/events", 12122495545),
	o("rsc/markdown", 14, "/issues/events", 12122501258),
	o("rsc/markdown", 14, "/issues/events", 12122508555),
	o("rsc/markdown", 15, "/issues", 2187046263),
	o("rsc/markdown", 16, "/issues", 2189605425),
	o("rsc/markdown", 16, "/issues/comments", 2146194902),
	o("rsc/markdown", 16, "/issues/events", 13027435265),
	o("rsc/markdown", 17, "/issues", 2189605911),
	o("rsc/markdown", 17, "/issues/comments", 2146194573),
	o("rsc/markdown", 17, "/issues/comments", 2146421109),
	o("rsc/markdown", 17, "/issues/events", 12137686933),
	o("rsc/markdown", 17, "/issues/events", 12137688071),
	o("rsc/markdown", 17, "/issues/events", 13027432818),
	o("rsc/markdown", 17, "/issues/events", 13028910699),
	o("rsc/markdown", 17, "/issues/events", 13028910702),
	o("rsc/markdown", 18, "/issues", 2276848742),
	o("rsc/markdown", 18, "/issues/comments", 2097019306),
	o("rsc/markdown", 18, "/issues/comments", 2146475274),
	o("rsc/markdown", 18, "/issues/events", 12721108829),
	o("rsc/markdown", 18, "/issues/events", 13027289256),
	o("rsc/markdown", 18, "/issues/events", 13027289270),
	o("rsc/markdown", 18, "/issues/events", 13027289466),
	o("rsc/markdown", 19, "/issues", 2308816936),
	o("rsc/markdown", 19, "/issues/comments", 2146197528),
}

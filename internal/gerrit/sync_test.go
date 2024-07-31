// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gerrit

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"golang.org/x/oscar/internal/httprr"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

// Only look at golang.org/x/sync. We picked the sync package
// because it isn't very active, it's just a coincidence that
// this test is also called TestSync.
const project = "sync"

func TestSync(t *testing.T) {
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	ctx := context.Background()

	rr, err := httprr.Open("testdata/sync.httprr", http.DefaultTransport)
	check(err)
	sdb := secret.Empty()
	c := New("go-review.googlesource.com", lg, db, sdb, rr.Client())

	check(c.Add(project))

	// Only look at changes before a certain date, so that we
	// don't get too much data.
	testBefore = "2016-08-01"
	defer func() { testBefore = "" }()

	check(c.Sync(ctx))

	var (
		gotCLs     []int
		first      bool = true
		firstCLNum int
		firstCL    *Change
	)
	for changeNum, chFn := range c.ChangeNumbers(project) {
		gotCLs = append(gotCLs, changeNum)
		if first {
			firstCLNum = changeNum
			firstCL = chFn()
			first = false
		}

		checkChange(t, c, changeNum, chFn())
	}

	wantCLs := []int{24894, 24907, 24961, 24962}
	if !slices.Equal(gotCLs, wantCLs) {
		t.Errorf("got CLs %v, want %v", gotCLs, wantCLs)
	}

	times, err := firstCL.Times()
	if err != nil {
		t.Error(err)
	} else {
		got := times.Created
		want := time.Date(2016, time.July, 13, 17, 50, 28, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("first CL %d created %v, want %v", firstCLNum, got, want)
		}
	}

	w := c.ChangeWatcher("test1")
	for e := range w.Recent() {
		w.MarkOld(e.DBTime)
	}

	// Now pretend that we are running later, and do an incremental update.

	testBefore = "2016-12-01"
	rr, err = httprr.Open("testdata/sync2.httprr", http.DefaultTransport)
	check(err)

	c = New("go-review.googlesource.com", lg, db, sdb, rr.Client())
	check(c.Sync(ctx))

	w = c.ChangeWatcher("test1")
	gotCLs = gotCLs[:0]
	for e := range w.Recent() {
		gotCLs = append(gotCLs, e.ChangeNum)
		ch := c.Change(project, e.ChangeNum)
		if ch == nil {
			t.Errorf("no data for CL %d", e.ChangeNum)
			continue
		}
		checkChange(t, c, e.ChangeNum, ch)
	}

	wantCLs = []int{30292}
	if !slices.Equal(gotCLs, wantCLs) {
		t.Errorf("incremental update got %v, want %v", gotCLs, wantCLs)
	}
}

// checkChange verifies that we can unpack CL information, and that it
// looks sane.
func checkChange(t *testing.T, c *Client, changeNum int, ch *Change) {
	// Verify that we can unpackage the change into a ChangeInfo.
	ci, err := ch.ChangeInfo()
	if err != nil {
		t.Error(err)
		return
	}

	if ci.Number != changeNum {
		t.Errorf("found CL %d in data for CL %d", ci.Number, changeNum)
	}

	// Fetch and unpack comments for the change.
	commentsInfo, err := c.Comments(project, changeNum)
	if err != nil {
		t.Error(err)
		return
	}
	if commentsInfo == nil {
		t.Errorf("no comment information for CL %d", changeNum)
		return
	}

	for _, comments := range commentsInfo {
		for _, comment := range comments {
			checkComment(t, ci, comment)
		}
	}
}

// checkComment verifies links from the comment back to the CL.
func checkComment(t *testing.T, ci *ChangeInfo, cmi *CommentInfo) {
	found := false
	for _, msg := range ci.Messages {
		if cmi.ChangeMessageID == msg.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("CL %d: did not find comment message ID %q in CL messages", ci.Number, cmi.ChangeMessageID)
	}

	found = false
	for rev := range ci.Revisions {
		if cmi.CommitID == rev {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("CL %d: did not find revision ID %q in CL revisions", ci.Number, cmi.CommitID)
	}
}

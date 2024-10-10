// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gerrit

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"strings"
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
	testNow = "2016-08-01"
	defer func() { testNow = "" }()

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

	checkFirstCL(t, c, firstCL, firstCLNum)

	w := c.ChangeWatcher("test1")
	for e := range w.Recent() {
		w.MarkOld(e.DBTime)
	}

	// Now pretend that we are running later, and do an incremental update.

	testNow = "2016-12-01"
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

// changeTests is a list of tests to run on a change.
type changeTests struct {
	name     string
	accessor func(*Change) any
	want     any
	eq       func(any, any) bool
}

// accessor is one of the accessor methods to retrieve Change values.
type accessor[T any] func(*Change) T

// wa wraps an accessor[T] in a function we can put in a table.
func wa[T any](fn accessor[T]) func(*Change) any {
	return func(ch *Change) any {
		return fn(ch)
	}
}

// testChangeTests checks that a [Change] satisfies a list of [changeTests].
func testChangeTests(t *testing.T, ch *Change, tests []changeTests) {
	t.Helper()
	for _, test := range tests {
		got := test.accessor(ch)
		var ok bool
		if test.eq == nil {
			ok = got == test.want
		} else {
			ok = test.eq(got, test.want)
		}
		if !ok {
			t.Errorf("%s got %v, want %v", test.name, got, test.want)
		}
	}
}

// checkFirstCL checks the first CL in our saved sync against
// the values we expect. The first CL is https://go.dev/cl/24894.
func checkFirstCL(t *testing.T, c *Client, ch *Change, num int) {
	if num != 24894 {
		t.Errorf("got first CL number %d, want 24894", num)
	}

	tests := []changeTests{
		{
			"ChangeNumber",
			wa(c.ChangeNumber),
			24894,
			nil,
		},
		{
			"ChangeStatus",
			wa(c.ChangeStatus),
			"MERGED",
			nil,
		},
		{
			"ChangeOwner",
			wa(c.ChangeOwner),
			"bcmills@google.com",
			func(got, want any) bool {
				return got.(*AccountInfo).Email == want
			},
		},
		{
			"ChangeSubmitter",
			wa(c.ChangeSubmitter),
			"bcmills@google.com",
			func(got, want any) bool {
				return got.(*AccountInfo).Email == want
			},
		},
		{
			"ChangeTimes",
			wa(c.ChangeTimes),
			ChangeTimes{
				Created:   time.Date(2016, time.July, 13, 17, 50, 28, 0, time.UTC),
				Updated:   time.Date(2016, time.July, 15, 18, 31, 27, 0, time.UTC),
				Submitted: time.Date(2016, time.July, 15, 18, 28, 34, 0, time.UTC),
			},
			func(got, want any) bool {
				g := got.(ChangeTimes)
				w := want.(ChangeTimes)
				return g.Created.Equal(w.Created) &&
					g.Updated.Equal(w.Updated) &&
					g.Submitted.Equal(w.Submitted) &&
					g.Abandoned.Equal(w.Abandoned)
			},
		},
		{
			"ChangeSubject",
			wa(c.ChangeSubject),
			"errgroup: add package",
			nil,
		},
		{
			"ChangeMessages",
			wa(c.ChangeMessages),
			[]string{
				"Uploaded patch set 1.",
				"Uploaded patch set 2.: Patch Set 1 was rebased",
				"Uploaded patch set 3.: Patch Set 2 was rebased",
				"Uploaded patch set 4.",
				"Patch Set 4:\n\nA new package in the standard library should probably have a proposal document (https://github.com/golang/proposal/blob/master/README.md).",
				"Patch Set 4:\n\n> A new package in the standard library should probably have a\n > proposal document (https://github.com/golang/proposal/blob/master/README.md).\n\nNote that this is in the x/sync repo, not the standard library.\n\n(I'd be happy if it made it into the standard library, and I'll write a proposal either way if you still think it's a good idea to do so.)",
				"Patch Set 4: Code-Review+1\n\n(3 comments)",
				"Patch Set 4:\n\n(5 comments)",
				"Patch Set 4:\n\n(1 comment)",
				"Uploaded patch set 5.",
				"Patch Set 4:\n\n(9 comments)",
				"Uploaded patch set 6.",
				"Patch Set 6:\n\n(1 comment)",
				"Uploaded patch set 7.",
				"Patch Set 6:\n\n(1 comment)",
				"Patch Set 7: Code-Review+2\n\n(1 comment)\n\nVery nice.  I like how Group simplifies the pipeline code.",
				"Patch Set 7: Run-TryBot+1 Code-Review+2\n\nMuch simpler now.",
				"Patch Set 7:\n\nTryBots beginning. Status page: http://farmer.golang.org/try?commit=69e997ae",
				"Patch Set 7:\n\nBuild is still in progress...\nThis change failed on darwin-amd64-10_10:\nSee https://storage.googleapis.com/go-build-log/53da5fd4/darwin-amd64-10_10_b0863636.log\n\nConsult https://build.golang.org/ to see whether it's a new failure. Other builds still in progress; subsequent failure notices suppressed until final report.",
				"Patch Set 7: TryBot-Result-1\n\n9 of 9 TryBots failed:\nFailed on darwin-amd64-10_10: https://storage.googleapis.com/go-build-log/53da5fd4/darwin-amd64-10_10_b0863636.log\nFailed on linux-amd64: https://storage.googleapis.com/go-build-log/53da5fd4/linux-amd64_6b41efba.log\nFailed on linux-386: https://storage.googleapis.com/go-build-log/53da5fd4/linux-386_c28bdc33.log\nFailed on windows-amd64-gce: https://storage.googleapis.com/go-build-log/53da5fd4/windows-amd64-gce_4c93aa91.log\nFailed on windows-386-gce: https://storage.googleapis.com/go-build-log/53da5fd4/windows-386-gce_ed6ed606.log\nFailed on openbsd-amd64-gce58: https://storage.googleapis.com/go-build-log/53da5fd4/openbsd-amd64-gce58_27709611.log\nFailed on freebsd-386-gce101: https://storage.googleapis.com/go-build-log/53da5fd4/freebsd-386-gce101_3cd197fa.log\nFailed on freebsd-amd64-gce101: https://storage.googleapis.com/go-build-log/53da5fd4/freebsd-amd64-gce101_22003800.log\nFailed on openbsd-386-gce58: https://storage.googleapis.com/go-build-log/53da5fd4/openbsd-386-gce58_bd17e0e0.log\n\nConsult https://build.golang.org/ to see whether they are new failures.",
				"Uploaded patch set 8.",
				"Patch Set 7:\n\n(1 comment)",
				"Patch Set 8:\n\nFix the compilation errors and kick-off the trybots again too.",
				"Change has been successfully cherry-picked as 457c5828408160d6a47e17645169cf8fa20218c4",
				"Patch Set 9:\n\n> Fix the compilation errors and kick-off the trybots again too.\n\nArgh, didn't notice the errors until I had hit Submit.  (I think the last time I ran the tests I had forgotten to save the buffer...)\n\nShould I revert, or send a fix?",
				"Patch Set 9:\n\nUm, you submitted tests which don't compile.",
				"Patch Set 9:\n\nNaah, just roll forward and fix the test.",
			},
			func(got, want any) bool {
				g := got.([]*ChangeMessageInfo)
				w := want.([]string)
				for i, m := range g {
					if m.Message != w[i] {
						return false
					}
				}
				return true
			},
		},
		{
			"ChangeDescription",
			wa(c.ChangeDescription),
			"errgroup: add package\n\nPackage errgroup provides synchronization, error propagation, and\nContext cancellation for groups of goroutines working on subtasks of a\ncommon task.\n\nChange-Id: Ic9e51f6f846124076bbff9d53b0f09dc7fc5f2f0\nReviewed-on: https://go-review.googlesource.com/24894\nReviewed-by: Sameer Ajmani <sameer@golang.org>\nReviewed-by: Brad Fitzpatrick <bradfitz@golang.org>\n",
			nil,
		},
		{
			"ChangeWorkInProgress",
			wa(c.ChangeWorkInProgress),
			false,
			nil,
		},
		{
			"ChangeReviewers",
			wa(c.ChangeReviewers),
			[]string{
				"bradfitz@golang.org",
				"iant@golang.org",
				"sameer@golang.org",
				"danp@danp.net",
				"gobot@golang.org",
				"bcmills@google.com",
			},
			func(got, want any) bool {
				g := got.([]*AccountInfo)
				w := want.([]string)
				for i, a := range g {
					if a.Email != w[i] {
						return false
					}
				}
				return true
			},
		},
		{
			"ChangeLabels",
			wa(c.ChangeLabels),
			map[string]string{
				"Code-Review":   "",
				"Hold":          "",
				"Run-TryBot":    "Used to start legacy TryBots.",
				"TryBot-Result": "Label for reporting legacy TryBot results.",
				"Auto-Submit":   "",
				"TryBot-Bypass": "",
				"Bot-Commit":    "",
				"Commit-Queue":  "Used to start LUCI TryBots.",
			},
			func(got, want any) bool {
				g := got.(map[string]*LabelInfo)
				w := want.(map[string]string)
				m := make(map[string]string)
				for k, l := range g {
					m[k] = l.Description
				}
				return maps.Equal(m, w)
			},
		},
		{
			"ChangeLabel",
			func(ch *Change) any {
				return c.ChangeLabel(ch, "Run-TryBot")
			},
			"Used to start legacy TryBots.",
			func(got, want any) bool {
				g := got.(*LabelInfo)
				w := want.(string)
				return g.Description == w
			},
		},
		{
			"ChangeCommitAuthor",
			func(ch *Change) any {
				return c.ChangeCommitAuthor(ch, 1)
			},
			"bcmills@google.com",
			func(got, want any) bool {
				g := got.(*GitPersonInfo)
				w := want.(string)
				return g.Email == w
			},
		},
		{
			"ChangeHashtags",
			wa(c.ChangeHashtags),
			[]string{},
			func(got, want any) bool {
				g := got.([]string)
				w := want.([]string)
				return slices.Equal(g, w)
			},
		},
		{
			"ChangeRevisions",
			wa(c.ChangeRevisions),
			"errgroup: add package",
			func(got, want any) bool {
				g := got.([]*RevisionInfo)
				if len(g) != 9 {
					return false
				}
				for _, r := range g {
					if r.Commit.Subject != want {
						return false
					}
				}
				return true
			},
		},
	}

	testChangeTests(t, ch, tests)

	total, unresolved := c.ChangeCommentCounts(ch)
	wantTotal, wantUnresolved := 22, 0
	if total != wantTotal || unresolved != wantUnresolved {
		t.Errorf("CommentCounts = %d, %d; want %d, %d", total, unresolved, wantTotal, wantUnresolved)
	}
}

// checkChange verifies that we can unpack CL information, and that it
// looks sane.
func checkChange(t *testing.T, c *Client, changeNum int, ch *Change) {
	// Verify that we can unpackage the change into a ChangeInfo.
	ci := c.ChangeInfo(ch)
	if ci.Number != changeNum {
		t.Errorf("found CL %d in data for CL %d", ci.Number, changeNum)
	}

	// Fetch and unpack comments for the change.
	commentsInfo := c.Comments(project, changeNum)
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

func TestSyncTesting(t *testing.T) {
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	ctx := context.Background()

	project := "test"
	numCLs := func(c *Client) int {
		cnt := 0
		for _, _ = range c.ChangeNumbers(project) {
			cnt++
		}
		return cnt
	}

	for _, d := range []struct {
		file          string
		wantInterrupt bool
	}{
		{"testdata/sametime.txt", false},
		{"testdata/uniquetimes.txt", false},
		// For added complexity, the interruption happens in
		// the segment of changes updated at the same time.
		{"testdata/interrupt.txt", true},
	} {
		t.Run(d.file, func(t *testing.T) {
			db := storage.MemDB()
			c := New("", lg, db, nil, nil)
			check(c.Add(project))

			tc := c.Testing()
			tc.setLimit(3)
			check(tc.LoadTxtar(d.file))

			err := c.Sync(ctx)
			if d.wantInterrupt {
				if err == nil || !strings.Contains(err.Error(), "test interrupt error") {
					t.Fatalf("want test interrupt error; got %v", err)
				}
				check(c.Sync(ctx)) // repeat without interruption
			} else if err != nil {
				t.Fatal(err)
			}

			wantCLs := len(tc.chs)
			if gotCLs := numCLs(c); gotCLs != wantCLs {
				t.Errorf("want %d CLs; got %d", wantCLs, gotCLs)
			}
		})
	}
}

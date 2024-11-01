// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package googlegroups

import (
	"context"
	"testing"

	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestSyncGoogleGroupsDocs(t *testing.T) {
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()

	c := New(lg, db, nil, nil)
	check(c.Add("test"))

	tc := c.Testing()
	tc.setLimit(1000)
	check(tc.LoadTxtar("testdata/convs.txt"))
	// Sync the changes so the watcher has
	// something to work with.
	check(c.Sync(context.Background()))

	dc := docs.New(lg, db)
	docs.Sync(dc, c)

	var want = []string{
		"https://groups.google.com/g/test/c/1",
		"https://groups.google.com/g/test/c/2",
		"https://groups.google.com/g/test/c/3",
	}
	for d := range dc.Docs("") {
		if len(want) == 0 {
			t.Fatalf("unexpected extra doc: %s", d.ID)
		}
		if d.ID != want[0] {
			t.Fatalf("doc mismatch: have %s, want %s", d.ID, want[0])
		}
		want = want[1:]
		if d.ID == ch1 {
			if d.Title != ch1Title {
				t.Errorf("#1 Title = %q, want %q", d.Title, ch1Title)
			}
			if d.Text != ch1Text {
				t.Errorf("#1 Text = %q, want %q", d.Text, ch1Text)
			}
		}
	}
	if len(want) > 0 {
		t.Fatalf("missing docs: %v", want)
	}

	dc.Add("https://groups.google.com/g/test/c/1", "OLD TITLE", "OLD TEXT")
	docs.Sync(dc, c)
	d, _ := dc.Get(ch1)
	if d.Title != "OLD TITLE" || d.Text != "OLD TEXT" {
		t.Errorf("Sync rewrote #1: Title=%q Text=%q, want OLD TITLE, OLD TEXT", d.Title, d.Text)
	}

	docs.Restart(c)
	docs.Sync(dc, c)
	d, _ = dc.Get(ch1)
	if d.Title == "OLD TITLE" || d.Text == "OLD TEXT" {
		t.Errorf("Restart+Sync did not rewrite #1: Title=%q Text=%q", d.Title, d.Text)
	}
}

var (
	ch1      = "https://groups.google.com/g/test/c/1"
	ch1Title = "goroutines"
	ch1Text  = "Opening message for conversation 1"
)

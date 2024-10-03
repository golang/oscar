// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gerritdocs

import (
	"context"
	"testing"

	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/gerrit"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestSync(t *testing.T) {
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	ctx := context.Background()

	gr := gerrit.New("go-review.googlesource.com", lg, db, nil, nil)
	check(gr.Testing().LoadTxtar("testdata/changes.txt"))

	check(gr.Add("test"))
	// Sync the changes so the watcher has
	// something to work with.
	check(gr.Sync(ctx))

	dc := docs.New(lg, db)
	check(Sync(ctx, lg, dc, gr, []string{"test"}))

	var want = []string{
		"https://go-review.googlesource.com/c/test/+/1#related-content",
		"https://go-review.googlesource.com/c/test/+/2#related-content",
		"https://go-review.googlesource.com/c/test/+/3#related-content",
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

	dc.Add("https://go-review.googlesource.com/c/test/+/1#related-content", "OLD TITLE", "OLD TEXT")
	check(Sync(ctx, lg, dc, gr, []string{"test"}))
	d, _ := dc.Get(ch1)
	if d.Title != "OLD TITLE" || d.Text != "OLD TEXT" {
		t.Errorf("Sync rewrote #1: Title=%q Text=%q, want OLD TITLE, OLD TEXT", d.Title, d.Text)
	}

	Restart(lg, gr)
	check(Sync(ctx, lg, dc, gr, []string{"test"}))
	d, _ = dc.Get(ch1)
	if d.Title == "OLD TITLE" || d.Text == "OLD TEXT" {
		t.Errorf("Restart+Sync did not rewrite #1: Title=%q Text=%q", d.Title, d.Text)
	}
}

var (
	ch1      = "https://go-review.googlesource.com/c/test/+/1#related-content"
	ch1Title = "this is change number 1"
	ch1Text  = "gerrit: test change\n\ncomment 1\n\nmessage 1\n\ncomment 2\n\nmessage 2"
)

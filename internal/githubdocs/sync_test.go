// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package githubdocs

import (
	"testing"

	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestMarkdown(t *testing.T) {
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	gh := github.New(lg, db, nil, nil)
	check(gh.Testing().LoadTxtar("../testdata/markdown.txt"))

	dc := docs.New(db)
	Sync(lg, dc, gh)

	var want = []string{
		"https://github.com/rsc/markdown/issues/1",
		"https://github.com/rsc/markdown/issues/10",
		"https://github.com/rsc/markdown/issues/11",
		"https://github.com/rsc/markdown/issues/12",
		"https://github.com/rsc/markdown/issues/13",
		"https://github.com/rsc/markdown/issues/14",
		"https://github.com/rsc/markdown/issues/15",
		"https://github.com/rsc/markdown/issues/16",
		"https://github.com/rsc/markdown/issues/17",
		"https://github.com/rsc/markdown/issues/18",
		"https://github.com/rsc/markdown/issues/19",
		"https://github.com/rsc/markdown/issues/2",
		"https://github.com/rsc/markdown/issues/3",
		"https://github.com/rsc/markdown/issues/4",
		"https://github.com/rsc/markdown/issues/5",
		"https://github.com/rsc/markdown/issues/6",
		"https://github.com/rsc/markdown/issues/7",
		"https://github.com/rsc/markdown/issues/8",
		"https://github.com/rsc/markdown/issues/9",
	}
	for d := range dc.Docs("") {
		if len(want) == 0 {
			t.Fatalf("unexpected extra doc: %s", d.ID)
		}
		if d.ID != want[0] {
			t.Fatalf("doc mismatch: have %s, want %s", d.ID, want[0])
		}
		want = want[1:]
		if d.ID == md1 {
			if d.Title != md1Title {
				t.Errorf("#1 Title = %q, want %q", d.Title, md1Title)
			}
			if d.Text != md1Text {
				t.Errorf("#1 Text = %q, want %q", d.Text, md1Text)
			}
		}
	}
	if len(want) > 0 {
		t.Fatalf("missing docs: %v", want)
	}

	dc.Add("https://github.com/rsc/markdown/issues/1", "OLD TITLE", "OLD TEXT")
	Sync(lg, dc, gh)
	d, _ := dc.Get(md1)
	if d.Title != "OLD TITLE" || d.Text != "OLD TEXT" {
		t.Errorf("Sync rewrote #1: Title=%q Text=%q, want OLD TITLE, OLD TEXT", d.Title, d.Text)
	}

	Restart(lg, gh)
	Sync(lg, dc, gh)
	d, _ = dc.Get(md1)
	if d.Title == "OLD TITLE" || d.Text == "OLD TEXT" {
		t.Errorf("Restart+Sync did not rewrite #1: Title=%q Text=%q", d.Title, d.Text)
	}
}

var (
	md1      = "https://github.com/rsc/markdown/issues/1"
	md1Title = "Support Github Emojis"
	md1Text  = "This is an issue for supporting github emojis, such as `:smile:` for \n😄 . There's a github page that gives a mapping of emojis to image \nfile names that we can parse the hex representation out of here: \nhttps://api.github.com/emojis.\n"
)

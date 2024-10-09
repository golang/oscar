// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package discussiondocs

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/oscar/internal/discussion"
	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestSync(t *testing.T) {
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	sdb := secret.Empty()
	db := storage.MemDB()
	ctx := context.Background()

	c := discussion.New(ctx, lg, sdb, db)
	project := "test/project"
	check(c.Add(project))

	d1 := &discussion.Discussion{
		Title: "A discussion",
		Body:  "A body",
	}
	d2 := &discussion.Discussion{
		Title: "Another discussion",
		Body:  "Another body",
	}
	c1 := &discussion.Comment{
		Body: "comment",
	}

	id := c.Testing().AddDiscussion(project, d1)
	_ = c.Testing().AddComment(project, id, c1) // ignored (comment)
	id2 := c.Testing().AddDiscussion(project, d2)

	dc := docs.New(lg, db)
	check(Sync(ctx, lg, dc, c))

	dURL := func(d int64) string { return fmt.Sprintf("https://github.com/test/project/discussions/%d", d) }
	got := slices.Collect(dc.Docs(""))
	want := []*docs.Doc{
		{ID: dURL(id), Title: d1.Title, Text: d1.Body},
		{ID: dURL(id2), Title: d2.Title, Text: d2.Body},
	}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(docs.Doc{}, "DBTime")); diff != "" {
		t.Errorf("Sync() mismatch (-want, +got):\n%s", diff)
	}

	u := dURL(id)
	dc.Add(u, "OLD TITLE", "OLD TEXT")
	check(Sync(ctx, lg, dc, c))
	d, _ := dc.Get(u)
	if d.Title != "OLD TITLE" || d.Text != "OLD TEXT" {
		t.Errorf("Sync rewrote: Title=%q Text=%q, want OLD TITLE, OLD TEXT", d.Title, d.Text)
	}
	latestBefore := Latest(c)

	Restart(lg, c)
	if lr := Latest(c); lr != 0 {
		t.Errorf("latest is not 0 after restart: %d", lr)
	}
	check(Sync(ctx, lg, dc, c))
	d, _ = dc.Get(u)
	if d.Title == "OLD TITLE" || d.Text == "OLD TEXT" {
		t.Errorf("Restart+Sync did not rewrite: Title=%q Text=%q", d.Title, d.Text)
	}
	latestAfter := Latest(c)

	if latestBefore != latestAfter {
		t.Errorf("latest mismatch before=%d, after=%d", latestBefore, latestAfter)
	}
}

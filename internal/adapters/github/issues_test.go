// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/httprr"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestIssueSource(t *testing.T) {
	ctx := context.Background()
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	a := New(lg, db, nil, nil)

	s := a.IssueSource()
	p := &github.Issue{
		URL:       "https://api.github.com/repos/org/repo/issues/17",
		HTMLURL:   "htmlURL",
		Number:    17,
		Title:     "title",
		CreatedAt: "",
		UpdatedAt: "",
		ClosedAt:  "",
		Body:      "body",
		State:     "open",
	}
	u := p.Updates()
	check(u.SetTitle("t2"))
	check(u.SetBody("b2"))
	check(s.Update(ctx, p, u))
	es := a.ic.Testing().Edits()
	if len(es) != 1 {
		t.Fatalf("got %d edits, want 1", len(es))
	}
	got := es[0]
	want := &github.TestingEdit{
		Project:      "org/repo",
		Issue:        17,
		IssueChanges: &github.IssueChanges{Title: "t2", Body: "b2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}

	a.ic.Testing().ClearEdits()
	c := &github.IssueComment{
		URL:  "https://api.github.com/repos/org/repo/issues/comments/3",
		Body: "before",
	}
	u = c.Updates()
	check(u.SetBody("after"))
	check(s.Update(ctx, c, u))
	es = a.ic.Testing().Edits()
	if len(es) != 1 {
		t.Fatalf("got %d edits, want 1", len(es))
	}
	got = es[0]
	want = &github.TestingEdit{
		Project:             "org/repo",
		Comment:             3,
		IssueCommentChanges: &github.IssueCommentChanges{Body: "after"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestIssueWatcher(t *testing.T) {
	// Verify that Adapter.IssueWatcher skips events.
	ctx := context.Background()
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()

	rr, err := httprr.Open("../../github/testdata/markdown.httprr", http.DefaultTransport)
	check(err)
	if rr.Recording() {
		t.Fatal("record from internal/github, not here")
	}
	rr.ScrubReq(github.Scrub)
	a := New(lg, db, secret.Empty(), rr.Client())
	check(a.AddProject("rsc/markdown"))
	check(a.Sync(ctx))

	// Count the number of each API.
	ew := a.ic.EventWatcher("ew")
	wantCounts := map[string]int{}
	for e := range ew.Recent() {
		if i, ok := e.Typed.(*github.Issue); ok && i.PullRequest != nil {
			wantCounts["PR"]++
		} else {
			wantCounts[e.API]++
		}
	}
	if wantCounts["/issues/events"] == 0 {
		t.Fatal("no events in underlying watcher")
	}
	if wantCounts["PR"] == 0 {
		t.Fatal("no PRs in underlying watchers")
	}

	// Now check that no events or PRs appear.
	delete(wantCounts, "/issues/events")
	delete(wantCounts, "PR")
	gotCounts := map[string]int{}
	iw := a.IssueWatcher("iw")
	for p := range iw.Recent() {
		switch p.(type) {
		case *github.Issue:
			gotCounts["/issues"]++
		case *github.IssueComment:
			gotCounts["/issues/comments"]++
		default:
			t.Fatalf("bad issue type %T", p)
		}
	}
	if !reflect.DeepEqual(gotCounts, wantCounts) {
		t.Errorf("got %v, want %v", gotCounts, wantCounts)
	}
}

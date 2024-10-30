// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/httprr"
	"golang.org/x/oscar/internal/model"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
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

	// Collect summaries of all events.
	allItems := recentItemsFromEvents(a.ic.EventWatcher("ew"))
	// The items from the issue watcher should be the subsequence of allItems that
	// are issues and issue comments.
	var wantItems []item
	sawIssue := false
	sawComment := false
	for _, it := range allItems {
		if it.API == "/issues" {
			wantItems = append(wantItems, it)
			sawIssue = true
		} else if it.API == "/issues/comments" {
			wantItems = append(wantItems, it)
			sawComment = true
		}
	}
	if !sawIssue || !sawComment {
		t.Fatal("missing at least one issue and one issue comment")
	}

	issueItems := recentItemsFromPosts(a.IssueWatcher("iw"))
	if diff := cmp.Diff(wantItems, issueItems); diff != "" {
		t.Errorf("mismatch (-want, +got):\n%s", diff)
	}
}

// item is a summary of a github.Event or model.DBContent, for testing.
type item struct {
	DBTime timed.DBTime
	Issue  int64
	API    string // github API, or "PR" for pull requests
	ID     int64
}

func recentItemsFromEvents(w *timed.Watcher[*github.Event]) []item {
	var its []item
	for e := range w.Recent() {
		it := item{
			DBTime: e.DBTime,
			Issue:  e.Issue,
			API:    e.API,
			ID:     e.ID,
		}
		if i, ok := e.Typed.(*github.Issue); ok {
			it.ID = i.Number
			if i.PullRequest != nil {
				it.API = "PR"
			}
		} else if c, ok := e.Typed.(*github.IssueComment); ok {
			it.ID = c.CommentID()
		}
		its = append(its, it)
	}
	return its
}

func recentItemsFromPosts(w model.Watcher[model.DBContent]) []item {
	var its []item
	for dp := range w.Recent() {
		it := item{DBTime: dp.DBTime}
		switch x := dp.Content.(type) {
		case *github.Issue:
			it.API = "/issues"
			it.Issue = x.Number
			it.ID = x.Number
		case *github.IssueComment:
			it.API = "/issues/comments"
			it.Issue = x.Issue()
			it.ID = x.CommentID()
		default:
			panic("bad post type")
		}
		its = append(its, it)
	}
	return its
}

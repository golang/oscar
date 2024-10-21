// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"context"
	"reflect"
	"testing"

	"golang.org/x/oscar/internal/github"
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
	check(s.Update(ctx, p, map[string]any{
		"title": "t2",
		"body":  "b2",
	}))
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
	check(s.Update(ctx, c, map[string]any{"body": "after"}))
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

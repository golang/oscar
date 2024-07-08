// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"net/http"
	"slices"
	"testing"

	"golang.org/x/oscar/internal/httprr"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestMarkdownEditing(t *testing.T) {
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()

	// Initial load.
	rr, err := httprr.Open("testdata/tmpedit.httprr", http.DefaultTransport)
	check(err)
	rr.Scrub(Scrub)
	sdb := secret.DB(secret.Map{"api.github.com": "user:pass"})
	if rr.Recording() {
		sdb = secret.Netrc()
	}
	c := New(lg, db, sdb, rr.Client())
	check(c.Add("rsc/tmp"))
	check(c.Sync())

	var ei, ec *Event
	for e := range c.Events("rsc/tmp", 5, 5) {
		if ei == nil && e.API == "/issues" {
			ei = e
		}
		if ec == nil && e.API == "/issues/comments" {
			ec = e
		}
	}
	if ei == nil {
		t.Fatalf("did not find issue #5")
	}
	if ec == nil {
		t.Fatalf("did not find comment on issue #5")
	}

	issue := ei.Typed.(*Issue)
	issue1, err := c.DownloadIssue(issue.URL)
	check(err)
	if issue1.Title != issue.Title {
		t.Errorf("DownloadIssue: Title=%q, want %q", issue1.Title, issue.Title)
	}

	comment := ec.Typed.(*IssueComment)
	comment1, err := c.DownloadIssueComment(comment.URL)
	check(err)
	if comment1.Body != comment.Body {
		t.Errorf("DownloadIssueComment: Body=%q, want %q", comment1.Body, comment.Body)
	}

	c.testing = false // edit github directly (except for the httprr in the way)
	check(c.EditIssueComment(comment, &IssueCommentChanges{Body: rot13(comment.Body)}))
	check(c.PostIssueComment(issue, &IssueCommentChanges{Body: "testing. rot13 is the best."}))
	check(c.EditIssue(issue, &IssueChanges{Title: rot13(issue.Title)}))
}

func TestMarkdownDivertEdit(t *testing.T) {
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()

	c := New(lg, db, nil, nil)
	check(c.Testing().LoadTxtar("../testdata/rsctmp.txt"))

	var ei, ec *Event
	for e := range c.Events("rsc/tmp", 5, 5) {
		if ei == nil && e.API == "/issues" {
			ei = e
		}
		if ec == nil && e.API == "/issues/comments" {
			ec = e
		}
	}
	if ei == nil {
		t.Fatalf("did not find issue #5")
	}
	if ec == nil {
		t.Fatalf("did not find comment on issue #5")
	}

	issue := ei.Typed.(*Issue)
	issue1, err := c.DownloadIssue(issue.URL)
	check(err)
	if issue1.Title != issue.Title {
		t.Errorf("DownloadIssue: Title=%q, want %q", issue1.Title, issue.Title)
	}

	comment := ec.Typed.(*IssueComment)
	comment1, err := c.DownloadIssueComment(comment.URL)
	check(err)
	if comment1.Body != comment.Body {
		t.Errorf("DownloadIssueComment: Body=%q, want %q", comment1.Body, comment.Body)
	}

	check(c.EditIssueComment(comment, &IssueCommentChanges{Body: rot13(comment.Body)}))
	check(c.PostIssueComment(issue, &IssueCommentChanges{Body: "testing. rot13 is the best."}))
	check(c.EditIssue(issue, &IssueChanges{Title: rot13(issue.Title), Labels: &[]string{"ebg13"}}))

	var edits []string
	for _, e := range c.Testing().Edits() {
		edits = append(edits, e.String())
	}

	want := []string{
		`EditIssueComment(rsc/tmp#5.10000000008, {"body":"Comment!\n"})`,
		`PostIssueComment(rsc/tmp#5, {"body":"testing. rot13 is the best."})`,
		`EditIssue(rsc/tmp#5, {"title":"another new issue","labels":["ebg13"]})`,
	}
	if !slices.Equal(edits, want) {
		t.Fatalf("Testing().Edits():\nhave %s\nwant %s", edits, want)
	}
}

func rot13(s string) string {
	b := []byte(s)
	for i, x := range b {
		if 'A' <= x && x <= 'M' || 'a' <= x && x <= 'm' {
			b[i] = x + 13
		} else if 'N' <= x && x <= 'Z' || 'n' <= x && x <= 'z' {
			b[i] = x - 13
		}
	}
	return string(b)
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/oscar/internal/httprr"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/llmapp"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestIssueOverview(t *testing.T) {
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()

	rr, err := httprr.Open("testdata/ivy.httprr", http.DefaultTransport)
	check(err)
	rr.ScrubReq(Scrub)
	sdb := secret.Empty()
	if rr.Recording() {
		sdb = secret.Netrc()
	}
	c := New(lg, db, sdb, rr.Client())
	check(c.Add("robpike/ivy"))
	check(c.Sync(ctx))

	echo := llm.EchoTextGenerator()

	got, err := IssueOverview(ctx, echo, db, "robpike/ivy", 19)
	if err != nil {
		t.Fatal(err)
	}

	issue := &Issue{
		URL:     "https://api.github.com/repos/robpike/ivy/issues/19",
		HTMLURL: "https://github.com/robpike/ivy/issues/19",
		User:    User{Login: "xunshicheng"},
		Title:   "cannot get",
		Body: `when i get the source code by the command: go get github.com/robpike/ivy
it print: can't load package: package github.com/robpike/ivy: code in directory D:\gocode\src\github.com\robpike\ivy expects import "robpike.io/ivy"

could you get me a hand！
`,
		Number:    19,
		CreatedAt: "2016-01-05T11:57:53Z",
		UpdatedAt: "2016-01-05T22:39:41Z",
		ClosedAt:  "2016-01-05T22:39:41Z",
		Assignees: []User{},
		State:     "closed",
		Labels:    []Label{},
	}

	// This merely checks that the correct call to [llmapp.PostOverview] is made.
	// The internals of [llmapp.PostOverview] are tested in the llmapp package.
	wantOverview, err := llmapp.PostOverview(ctx, echo,
		&llmapp.Doc{
			Type:   "issue",
			URL:    "https://github.com/robpike/ivy/issues/19",
			Author: "xunshicheng",
			Title:  issue.Title,
			Text:   issue.Body,
		},
		[]*llmapp.Doc{
			{
				Type:   "issue comment",
				URL:    "https://github.com/robpike/ivy/issues/19#issuecomment-169157303",
				Author: "robpike",
				Text: `See the import comment, or listen to the error message. Ivy uses a custom import.

go get robpike.io/ivy

It is a fair point though that this should be explained in the README. I will fix that.
`,
			},
		})
	if err != nil {
		t.Fatal(err)
	}

	want := &IssueOverviewResult{
		Issue:       issue,
		Overview:    wantOverview,
		NumComments: 1,
	}

	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("IssueOverview() mismatch:\n%s", diff)
	}
}

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

	got, err := IssueOverview(ctx, llm.EchoTextGenerator(), db, "robpike/ivy", 19)
	if err != nil {
		t.Fatal(err)
	}

	prompt := llmapp.OverviewPrompt(llmapp.PostAndComments, []*llmapp.Doc{
		{
			Type:   "issue",
			URL:    "https://github.com/robpike/ivy/issues/19",
			Author: "xunshicheng",
			Title:  "cannot get",
			Text: `when i get the source code by the command: go get github.com/robpike/ivy
it print: can't load package: package github.com/robpike/ivy: code in directory D:\gocode\src\github.com\robpike\ivy expects import "robpike.io/ivy"

could you get me a hand！
`,
		},
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
	overview := llm.EchoResponse(prompt...)

	want := &IssueOverviewResult{
		URL:         "https://github.com/robpike/ivy/issues/19",
		Overview:    overview,
		NumComments: 1,
		Prompt:      prompt,
	}

	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("IssueOverview() mismatch:\n%s", diff)
	}
}

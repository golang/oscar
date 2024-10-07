// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package discussion

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/httprr"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/testutil"
)

func TestDiscussions(t *testing.T) {
	ctx := context.Background()
	c := testGQLClient(t)
	owner := "tatianab"
	repo := "scratch"
	// Restrict items per page to ensure pagination works.
	restore := maxItemsPerPage
	maxItemsPerPage = 2
	t.Cleanup(func() { maxItemsPerPage = restore })
	var got []*Discussion
	for d, err := range c.discussions(ctx, owner, repo) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, d)
	}
	want := []*Discussion{
		{
			URL:              "https://github.com/tatianab/scratch/discussions/51",
			Number:           51,
			Author:           github.User{Login: "tatianab"},
			Title:            "A general discussion",
			CreatedAt:        "2024-10-07T16:08:25Z",
			UpdatedAt:        "2024-10-07T16:30:38Z",
			LastEditedAt:     "2024-10-07T16:30:38Z",
			Body:             "Some locked topic of discussion.",
			UpvoteCount:      1,
			Locked:           true,
			ActiveLockReason: "RESOLVED",
			Labels:           nil,
		},
		{
			URL:              "https://github.com/tatianab/scratch/discussions/52",
			Number:           52,
			Author:           github.User{Login: "tatianab"},
			Title:            "A third discussion",
			CreatedAt:        "2024-10-07T16:09:40Z",
			UpdatedAt:        "2024-10-07T16:20:27Z",
			LastEditedAt:     "2024-10-07T16:20:27Z",
			Body:             "So much discussing to do.\r\n\r\nThere's always more to talk about.",
			UpvoteCount:      1,
			Locked:           false,
			ActiveLockReason: "",
			Labels:           nil,
		},
		{
			URL:              "https://github.com/tatianab/scratch/discussions/50",
			Number:           50,
			Author:           github.User{Login: "tatianab"},
			Title:            "Welcome to discussions",
			CreatedAt:        "2024-10-07T16:06:05Z",
			UpdatedAt:        "2024-10-07T16:07:27Z",
			LastEditedAt:     "",
			Body:             "This is an example of a discussion.\r\n",
			UpvoteCount:      1,
			Locked:           false,
			ActiveLockReason: "",
			Labels:           []github.Label{{Name: "other"}},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("discussions() mismatch (-want, +got):\n%s", diff)
	}
}

func TestComments(t *testing.T) {
	ctx := context.Background()
	// Restrict items per page to ensure pagination works.
	restore := maxItemsPerPage
	maxItemsPerPage = 2
	t.Cleanup(func() { maxItemsPerPage = restore })
	c := testGQLClient(t)
	owner := "tatianab"
	repo := "scratch"
	var got []*Comment
	for c, err := range c.comments(ctx, owner, repo) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, c)
	}
	want := []*Comment{
		{
			URL:           "https://github.com/tatianab/scratch/discussions/51#discussioncomment-10870149",
			DiscussionURL: "https://github.com/tatianab/scratch/discussions/51",
			ReplyToURL:    "",
			Author:        github.User{Login: "tatianab"},
			CreatedAt:     "2024-10-07T16:08:32Z",
			UpdatedAt:     "2024-10-07T16:08:33Z",
			Body:          "A comment",
		},
		{
			URL:           "https://github.com/tatianab/scratch/discussions/51#discussioncomment-10870153",
			DiscussionURL: "https://github.com/tatianab/scratch/discussions/51",
			ReplyToURL:    "",
			Author:        github.User{Login: "tatianab"},
			CreatedAt:     "2024-10-07T16:08:39Z",
			UpdatedAt:     "2024-10-07T16:08:40Z",
			Body:          "Another comment!",
		},
		{
			URL:           "https://github.com/tatianab/scratch/discussions/51#discussioncomment-10870157",
			DiscussionURL: "https://github.com/tatianab/scratch/discussions/51",
			ReplyToURL:    "",
			Author:        github.User{Login: "tatianab"},
			CreatedAt:     "2024-10-07T16:08:47Z",
			UpdatedAt:     "2024-10-07T16:08:48Z",
			Body:          "Yet another comment.",
		},
		{
			URL:           "https://github.com/tatianab/scratch/discussions/51#discussioncomment-10870161",
			DiscussionURL: "https://github.com/tatianab/scratch/discussions/51",
			ReplyToURL:    "https://github.com/tatianab/scratch/discussions/51#discussioncomment-10870157",
			Author:        github.User{Login: "tatianab"},
			CreatedAt:     "2024-10-07T16:08:59Z",
			UpdatedAt:     "2024-10-07T16:09:00Z",
			Body:          "A reply",
		},
		{
			URL:           "https://github.com/tatianab/scratch/discussions/51#discussioncomment-10870165",
			DiscussionURL: "https://github.com/tatianab/scratch/discussions/51",
			ReplyToURL:    "https://github.com/tatianab/scratch/discussions/51#discussioncomment-10870157",
			Author:        github.User{Login: "tatianab"},
			CreatedAt:     "2024-10-07T16:09:08Z",
			UpdatedAt:     "2024-10-07T16:09:10Z",
			Body:          "A second reply",
		},
		{
			URL:           "https://github.com/tatianab/scratch/discussions/51#discussioncomment-10870169",
			DiscussionURL: "https://github.com/tatianab/scratch/discussions/51",
			ReplyToURL:    "https://github.com/tatianab/scratch/discussions/51#discussioncomment-10870157",
			Author:        github.User{Login: "tatianab"},
			CreatedAt:     "2024-10-07T16:09:15Z",
			UpdatedAt:     "2024-10-07T16:09:15Z",
			Body:          "A third reply",
		},
		{
			URL:           "https://github.com/tatianab/scratch/discussions/52#discussioncomment-10870178",
			DiscussionURL: "https://github.com/tatianab/scratch/discussions/52",
			ReplyToURL:    "",
			Author:        github.User{Login: "tatianab"},
			CreatedAt:     "2024-10-07T16:09:48Z",
			UpdatedAt:     "2024-10-07T16:09:49Z",
			Body:          "A comment.",
		},
		{
			URL:           "https://github.com/tatianab/scratch/discussions/50#discussioncomment-10870119",
			DiscussionURL: "https://github.com/tatianab/scratch/discussions/50",
			ReplyToURL:    "",
			Author:        github.User{Login: "tatianab"},
			CreatedAt:     "2024-10-07T16:07:01Z",
			UpdatedAt:     "2024-10-07T16:07:02Z",
			Body:          "This is a discussion comment.",
		},
		{
			URL:           "https://github.com/tatianab/scratch/discussions/50#discussioncomment-10870121",
			DiscussionURL: "https://github.com/tatianab/scratch/discussions/50",
			ReplyToURL:    "https://github.com/tatianab/scratch/discussions/50#discussioncomment-10870119",
			Author:        github.User{Login: "tatianab"},
			CreatedAt:     "2024-10-07T16:07:10Z",
			UpdatedAt:     "2024-10-07T16:07:10Z",
			Body:          "This is a discussion reply.",
		},
		{
			URL:           "https://github.com/tatianab/scratch/discussions/50#discussioncomment-10870125",
			DiscussionURL: "https://github.com/tatianab/scratch/discussions/50",
			ReplyToURL:    "https://github.com/tatianab/scratch/discussions/50#discussioncomment-10870119",
			Author:        github.User{Login: "tatianab"},
			CreatedAt:     "2024-10-07T16:07:19Z",
			UpdatedAt:     "2024-10-07T16:07:20Z",
			Body:          "This is another reply.",
		},
		{
			URL:           "https://github.com/tatianab/scratch/discussions/50#discussioncomment-10870127",
			DiscussionURL: "https://github.com/tatianab/scratch/discussions/50",
			ReplyToURL:    "",
			Author:        github.User{Login: "tatianab"},
			CreatedAt:     "2024-10-07T16:07:27Z",
			UpdatedAt:     "2024-10-07T16:07:28Z",
			Body:          "Another comment.",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("comments() mismatch (-want, +got):\n%s", diff)
	}
}

func testGQLClient(t *testing.T) *gqlClient {
	t.Helper()

	check := testutil.Checker(t)

	fname := fmt.Sprintf("testdata/gql/%s.httprr", t.Name())
	if _, err := os.Stat(fname); err != nil {
		dir := filepath.Dir(fname)
		check(os.MkdirAll(dir, os.ModePerm))
		_, err := os.Create(fname)
		check(err)
	}

	return testGQLClientFromFile(t, fname)
}

func testGQLClientFromFile(t *testing.T, fname string) *gqlClient {
	sdb := secret.DB(secret.Map{"api.github.com": "user:pass"})
	recording, err := httprr.Recording(fname)
	if err != nil {
		t.Fatal(err)
	}
	if recording {
		sdb = secret.Netrc()
	}

	ac := authClient(context.Background(), sdb)
	rr, err := httprr.Open(fname, ac.Transport)
	testutil.Check(t, err)
	rr.Scrub(github.Scrub)
	c := newGQLClient(rr.Client())
	return c
}

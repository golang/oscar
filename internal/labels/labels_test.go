// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package labels

import (
	"context"
	"regexp"
	"testing"

	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
)

func TestIssueLabels(t *testing.T) {
	ctx := context.Background()
	llm := kindTestGenerator()
	db := storage.MemDB()

	iss := &github.Issue{
		URL:   "https://api.github.com/repos/golang/go/issues/1",
		Title: "title",
		Body:  "body",
	}

	cat, exp, err := IssueCategory(ctx, db, llm, iss)
	if err != nil {
		t.Fatal(err)
	}
	got := response{cat.Name, exp}
	want := response{CategoryName: "other", Explanation: "whatever"}
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func kindTestGenerator() llm.ContentGenerator {
	return llm.TestContentGenerator(
		"kindTestGenerator",
		func(_ context.Context, schema *llm.Schema, promptParts []llm.Part) (string, error) {
			return `{"CategoryName":"other","Explanation":"whatever"}`, nil
		})
}

func TestCleanIssueBody(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"", ""},
		{"# H\nword\nword2\n", "# H\n\nword\nword2\n"},
		{
			"<!-- comment -->\n### H3\n<!-- another --> done",
			"\n\n### H3\n\n done\n",
		},
		{
			"<!--\ncomment\n-->\n### H3\n<!-- another -->\ndone",
			"\n\n### H3\n\n\n\ndone\n",
		},
		{
			"<!-- a --> b -->",
			" b -->\n",
		},
	} {
		got := cleanIssueBody(github.ParseMarkdown(tc.in))
		if got != tc.want {
			t.Errorf("%q:\ngot  %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

func TestHasText(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"", false},
		{"something", true},
		{"# just a heading", false},
		{"## head\nx", true},
	} {
		got := hasText(github.ParseMarkdown(tc.in))
		if got != tc.want {
			t.Errorf("%q: got %t, want %t", tc.in, got, tc.want)
		}
	}
}

func TestBuildPrompt(t *testing.T) {
	cat := Category{
		Name:        "cat",
		Description: "desc",
		Label:       "lab",
		Extra:       "extra",
	}
	ex := Example{
		Title:    "extitle",
		Body:     "exbody",
		Category: "excat",
	}
	got, err := buildPrompt("title", "body", []Category{cat}, []Example{ex})
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"title", "body", "categorize", "cat",
		"desc", "extra", "extitle", "exbody", "excat"} {
		if !regexp.MustCompile(`\b` + word + `\b`).MatchString(got) {
			t.Errorf("missing %q", word)
		}
	}
	if t.Failed() {
		t.Logf("got: %s", got)
	}
}

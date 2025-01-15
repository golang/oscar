// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rules

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/oscar/internal/actions"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestIssue(t *testing.T) {
	ctx := context.Background()
	db := storage.MemDB()
	llm := ruleTestGenerator()

	// Construct a test issue.
	i := new(github.Issue)
	i.URL = "https://api.github.com/repos/golang/go/" // for project name
	i.Number = 999
	i.User = github.User{Login: "user"}
	i.Title = "title"
	i.Body = "body"

	// Ask about rule violations.
	r, err := Issue(ctx, db, llm, i, false)
	if err != nil {
		t.Fatalf("IssueRules failed with %v", err)
	}

	// Check result.
	if !strings.Contains(r.Response, "\n- The issue title must start") {
		t.Errorf("expected the issue title rule failed, but it didn't. Total output: %s", r.Response)
	}
	if n := strings.Count(r.Response, "\n- "); n != 1 {
		t.Errorf("wanted 1 rule violation, got %d", n)
	}
}

func TestClassify(t *testing.T) {
	ctx := context.Background()
	db := storage.MemDB()
	llm := ruleTestGenerator()

	// Construct a test issue.
	i := new(github.Issue)
	i.URL = "https://api.github.com/repos/golang/go/" // for project name
	i.Number = 999
	i.User = github.User{Login: "user"}
	i.Title = "title"
	i.Body = "body"

	// Run classifier.
	r, _, err := Classify(ctx, db, llm, i)
	if err != nil {
		t.Fatalf("Classify failed with %v", err)
	}

	// Check result.
	want := "bug"
	if r.Name != want {
		t.Errorf("Classify got %q, want %q", r.Name, want)
	}
}

func ruleTestGenerator() llm.ContentGenerator {
	return llm.TestContentGenerator(
		"ruleTestGenerator",
		func(_ context.Context, schema *llm.Schema, promptParts []llm.Part) (string, error) {
			var strs []string
			for _, p := range promptParts {
				strs = append(strs, string(p.(llm.Text)))
			}
			req := strings.Join(strs, " ")
			if strings.Contains(req, "Your job is to categorize") {
				// categorize request. Always report it as a "bug".
				return `{"CategoryName":"bug","Explanation":"I think this is a bug."}`, nil
			}
			if strings.Contains(req, "Your job is to decide") {
				// rule request. Report that the title rule failed and the others succeeded.
				if strings.Contains(req, "The issue title must start") {
					return "no\nThe title is malformed.", nil
				}
				return "yes\nThe rule is obeyed.", nil

			}
			return "UNK", nil
		})
}

func TestRun(t *testing.T) {
	ctx := context.Background()
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	gh := github.New(lg, db, nil, nil)
	llm := ruleTestGenerator()

	tc := gh.Testing()
	testIssue := &github.Issue{
		Number:    888,
		Title:     "title",
		Body:      "body",
		CreatedAt: time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339),
	}
	tc.AddIssue("golang/go", testIssue)

	p := New(lg, db, gh, llm, "postname")
	p.EnableProject("golang/go")
	p.EnablePosts()

	check(p.Run(ctx))
	check(actions.Run(ctx, lg, db))

	entries := slices.Collect(actions.ScanAfter(lg, db, time.Time{}, nil))
	if len(entries) != 1 {
		t.Fatalf("too many action entries. Got %d, want 1", len(entries))
	}
	e := entries[0]

	var a action
	if err := json.Unmarshal(e.Action, &a); err != nil {
		t.Fatal(err)
	}
	if got, want := a.Issue.Project(), "golang/go"; got != want {
		t.Fatalf("posted to unexpected project: got %s, want %s", got, want)
		return
	}
	if got, want := a.Issue.Number, int64(888); got != want {
		t.Fatalf("posted to unexpected issue: got %d, want %d", got, want)
	}
	wantBody := strings.TrimSpace(`
We've identified some possible problems with your issue. Please review
these findings and fix any that you think are appropriate to fix.

- The issue title must start with a package name followed by a colon.


I'm just a bot; you probably know better than I do whether these findings really need fixing.
<sub>(Emoji vote if this was helpful or unhelpful; more detailed feedback welcome in [this discussion](https://github.com/golang/go/discussions/67901).)</sub>
`)
	if got := strings.TrimSpace(a.Changes.Body); got != wantBody {
		t.Fatalf("body wrong: got %s, want %s", got, wantBody)
	}
}

func TestOld(t *testing.T) {
	ctx := context.Background()
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	gh := github.New(lg, db, nil, nil)
	llm := ruleTestGenerator()

	tc := gh.Testing()
	testIssue := &github.Issue{
		Number:    888,
		Title:     "title",
		Body:      "body",
		CreatedAt: time.Now().Add(-100 * time.Hour).UTC().Format(time.RFC3339),
	}
	tc.AddIssue("golang/go", testIssue)

	p := New(lg, db, gh, llm, "postname")
	p.EnableProject("golang/go")
	p.EnablePosts()

	check(p.Run(ctx))
	check(actions.Run(ctx, lg, db))

	entries := slices.Collect(actions.ScanAfter(lg, db, time.Time{}, nil))
	if len(entries) != 0 {
		t.Fatalf("too many action entries. Got %d, want 0", len(entries))
	}
}

func TestClosed(t *testing.T) {
	ctx := context.Background()
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	gh := github.New(lg, db, nil, nil)
	llm := ruleTestGenerator()

	tc := gh.Testing()
	testIssue := &github.Issue{
		Number:    888,
		Title:     "title",
		Body:      "body",
		CreatedAt: time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339),
		State:     "closed",
	}
	tc.AddIssue("golang/go", testIssue)

	p := New(lg, db, gh, llm, "postname")
	p.EnableProject("golang/go")
	p.EnablePosts()

	check(p.Run(ctx))
	check(actions.Run(ctx, lg, db))

	entries := slices.Collect(actions.ScanAfter(lg, db, time.Time{}, nil))
	if len(entries) != 0 {
		t.Fatalf("too many action entries. Got %d, want 0", len(entries))
	}
}

func TestProjectFilter(t *testing.T) {
	ctx := context.Background()
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	gh := github.New(lg, db, nil, nil)
	llm := ruleTestGenerator()

	tc := gh.Testing()
	testIssue := &github.Issue{
		Number:    888,
		Title:     "title",
		Body:      "body",
		CreatedAt: time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339),
	}
	tc.AddIssue("golang/go1", testIssue)

	p := New(lg, db, gh, llm, "postname")
	p.EnableProject("golang/go2")
	p.EnablePosts()

	check(p.Run(ctx))
	check(actions.Run(ctx, lg, db))

	entries := slices.Collect(actions.ScanAfter(lg, db, time.Time{}, nil))
	if len(entries) != 0 {
		t.Fatalf("too many action entries. Got %d, want 0", len(entries))
	}
}

func TestRegexpMatch(t *testing.T) {
	ctx := context.Background()
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	gh := github.New(lg, db, nil, nil)
	llm := ruleTestGenerator()

	tc := gh.Testing()
	testIssue := &github.Issue{
		Number:    888,
		Title:     "title",
		Body:      "Hello. ### What did you expect to see?\n\n### Goodbye.",
		CreatedAt: time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339),
	}
	tc.AddIssue("golang/go", testIssue)

	p := New(lg, db, gh, llm, "postname")
	p.EnableProject("golang/go")
	p.EnablePosts()

	check(p.Run(ctx))
	check(actions.Run(ctx, lg, db))

	entries := slices.Collect(actions.ScanAfter(lg, db, time.Time{}, nil))
	if len(entries) != 1 {
		t.Fatalf("too many action entries. Got %d, want 1", len(entries))
	}
	e := entries[0]

	var a action
	if err := json.Unmarshal(e.Action, &a); err != nil {
		t.Fatal(err)
	}
	if got, want := a.Issue.Project(), "golang/go"; got != want {
		t.Fatalf("posted to unexpected project: got %s, want %s", got, want)
		return
	}
	if got, want := a.Issue.Number, int64(888); got != want {
		t.Fatalf("posted to unexpected issue: got %d, want %d", got, want)
	}
	wantBody := strings.TrimSpace(`
We've identified some possible problems with your issue. Please review
these findings and fix any that you think are appropriate to fix.

- The issue title must start with a package name followed by a colon.

- The "What did you expect to see?" section should not be empty.


I'm just a bot; you probably know better than I do whether these findings really need fixing.
<sub>(Emoji vote if this was helpful or unhelpful; more detailed feedback welcome in [this discussion](https://github.com/golang/go/discussions/67901).)</sub>
`)
	if got := strings.TrimSpace(a.Changes.Body); got != wantBody {
		t.Fatalf("body wrong: got %s, want %s", got, wantBody)
	}

}

func TestRegexpNotMatch(t *testing.T) {
	ctx := context.Background()
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	gh := github.New(lg, db, nil, nil)
	llm := ruleTestGenerator()

	tc := gh.Testing()
	testIssue := &github.Issue{
		Number:    888,
		Title:     "title",
		Body:      "Hello. ### What did you expect to see?\n\nSomething\n\n### Goodbye.",
		CreatedAt: time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339),
	}
	tc.AddIssue("golang/go", testIssue)

	p := New(lg, db, gh, llm, "postname")
	p.EnableProject("golang/go")
	p.EnablePosts()

	check(p.Run(ctx))
	check(actions.Run(ctx, lg, db))

	entries := slices.Collect(actions.ScanAfter(lg, db, time.Time{}, nil))
	if len(entries) != 1 {
		t.Fatalf("too many action entries. Got %d, want 1", len(entries))
	}
	e := entries[0]

	var a action
	if err := json.Unmarshal(e.Action, &a); err != nil {
		t.Fatal(err)
	}
	if got, want := a.Issue.Project(), "golang/go"; got != want {
		t.Fatalf("posted to unexpected project: got %s, want %s", got, want)
		return
	}
	if got, want := a.Issue.Number, int64(888); got != want {
		t.Fatalf("posted to unexpected issue: got %d, want %d", got, want)
	}
	wantBody := strings.TrimSpace(`
We've identified some possible problems with your issue. Please review
these findings and fix any that you think are appropriate to fix.

- The issue title must start with a package name followed by a colon.


I'm just a bot; you probably know better than I do whether these findings really need fixing.
<sub>(Emoji vote if this was helpful or unhelpful; more detailed feedback welcome in [this discussion](https://github.com/golang/go/discussions/67901).)</sub>
`)
	if got := strings.TrimSpace(a.Changes.Body); got != wantBody {
		t.Fatalf("body wrong: got %s, want %s", got, wantBody)
	}

}

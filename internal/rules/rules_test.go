// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rules

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/llm"
)

func TestIssue(t *testing.T) {
	ctx := context.Background()
	llm := ruleTestGenerator()

	// Construct a test issue.
	i := new(github.Issue)
	i.Number = 999
	i.User = github.User{Login: "user"}
	i.Title = "title"
	i.Body = "body"

	// Ask about rule violations.
	r, err := Issue(ctx, llm, i)
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
	llm := ruleTestGenerator()

	// Construct a test issue.
	i := new(github.Issue)
	i.Number = 999
	i.User = github.User{Login: "user"}
	i.Title = "title"
	i.Body = "body"

	// Run classifier.
	r, _, err := Classify(ctx, llm, i)
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

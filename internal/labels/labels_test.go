// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package labels

import (
	"context"
	"testing"

	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/llm"
)

func TestIssueLabels(t *testing.T) {
	ctx := context.Background()
	llm := kindTestGenerator()

	iss := &github.Issue{
		Title: "title",
		Body:  "body",
	}

	cat, exp, err := IssueCategory(ctx, llm, iss)
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

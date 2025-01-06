// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llmapp

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestWithChecker(t *testing.T) {
	lg := testutil.Slogger(t)
	g := llm.EchoContentGenerator()
	db := storage.MemDB()
	checker := badChecker{}
	c := NewWithChecker(lg, g, checker, db)

	// With violation.
	doc1 := &Doc{URL: "https://example.com", Author: "rsc", Title: "title", Text: "some bad text"}
	doc2 := &Doc{Text: "some good text 2"}
	r, err := c.Overview(context.Background(), doc1, doc2)
	if err != nil {
		t.Fatal(err)
	}
	if !r.HasPolicyViolation() {
		t.Errorf("c.Overview.HasPolicyViolation = false, want true")
	}
	want := &PolicyEvaluation{
		Violative: true,
		PromptResults: []*PolicyResult{
			// doc1
			{
				Results:    []*llm.PolicyResult{violationResult},
				Violations: []*llm.PolicyResult{violationResult},
			},
			// doc2
			{Results: []*llm.PolicyResult{okResult}},
			// instructions
			{Results: []*llm.PolicyResult{okResult}},
		},
		OutputResults: &PolicyResult{
			Results:    []*llm.PolicyResult{violationResult},
			Violations: []*llm.PolicyResult{violationResult},
		},
	}
	if diff := cmp.Diff(want, r.PolicyEvaluation, cmpopts.IgnoreFields(PolicyResult{}, "Text")); diff != "" {
		t.Errorf("c.Overview.PolicyEvaluation mismatch (-want,+got):\n%v", diff)
	}

	// Without violation.
	r, err = c.Overview(context.Background(), doc2)
	if err != nil {
		t.Fatal(err)
	}
	if r.HasPolicyViolation() {
		t.Errorf("c.Overview.HasPolicyViolation = true, want false")
	}

	want = &PolicyEvaluation{
		Violative: false,
		PromptResults: []*PolicyResult{
			// doc2
			{Results: []*llm.PolicyResult{okResult}},
			// instructions
			{Results: []*llm.PolicyResult{okResult}},
		},
		OutputResults: &PolicyResult{
			Results: []*llm.PolicyResult{okResult},
		},
	}
	if diff := cmp.Diff(want, r.PolicyEvaluation, cmpopts.IgnoreFields(PolicyResult{}, "Text")); diff != "" {
		t.Errorf("c.Overview.PolicyEvaluation mismatch (-want,+got):\n%v", diff)
	}
}

// badChecker is a test implementation of [llm.PolicyChecker] that
// always returns a policy violation of type "dangerous content"
// for text containing the string "bad", and no violations otherwise.
type badChecker struct{}

var _ llm.PolicyChecker = (*badChecker)(nil)

func (badChecker) Name() string {
	return "badchecker"
}

func (badChecker) Policies() []*llm.PolicyConfig {
	return []*llm.PolicyConfig{
		{PolicyType: llm.PolicyTypeDangerousContent},
	}
}

var (
	violationResult = &llm.PolicyResult{
		PolicyType:      llm.PolicyTypeDangerousContent,
		ViolationResult: llm.ViolationResultViolative,
		Score:           1,
	}
	okResult = &llm.PolicyResult{
		PolicyType:      llm.PolicyTypeDangerousContent,
		ViolationResult: llm.ViolationResultNonViolative,
		Score:           0,
	}
)

// return violation for text containing "bad" and no violation for any other text.
func (badChecker) CheckText(_ context.Context, text string, prompts ...llm.Part) ([]*llm.PolicyResult, error) {
	if strings.Contains(text, "bad") {
		return []*llm.PolicyResult{
			violationResult,
		}, nil
	}
	return []*llm.PolicyResult{
		okResult,
	}, nil
}

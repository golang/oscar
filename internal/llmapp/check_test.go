// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llmapp

import (
	"context"
	"strings"
	"testing"

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
	if !r.HasPolicyViolation {
		t.Errorf("c.Overview.HasPolicyViolation = false, want true")
	}

	// Without violation.
	r, err = c.Overview(context.Background(), doc2)
	if err != nil {
		t.Fatal(err)
	}
	if r.HasPolicyViolation {
		t.Errorf("c.Overview.HasPolicyViolation = true, want false")
	}
}

// badChecker is a test implementation of [llm.PolicyChecker] that
// always returns a policy violation for text containing the string "bad",
// and no violations otherwise.
type badChecker struct{}

// no-op
func (badChecker) SetPolicies(_ []*llm.PolicyConfig) {}

// return violation for text containing "bad" and no violation for any other text.
func (badChecker) CheckText(_ context.Context, text string, prompts ...llm.Part) ([]*llm.PolicyResult, error) {
	if strings.Contains(text, "bad") {
		return []*llm.PolicyResult{
			{
				PolicyType:      llm.PolicyTypeDangerousContent,
				ViolationResult: llm.ViolationResultViolative,
			},
		}, nil
	}
	return []*llm.PolicyResult{
		{
			PolicyType:      llm.PolicyTypeDangerousContent,
			ViolationResult: llm.ViolationResultNonViolative,
		},
	}, nil
}

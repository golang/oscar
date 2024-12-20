// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llmapp

import (
	"context"
	"fmt"
	"log/slog"

	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
)

// NewWithChecker is like [New], but it configures the Client to use
// the given checker to check the inputs to and outputs of the LLM against
// safety policies.
//
// When any of the Overview functions are called, the prompts and outputs of the LLM
// will be checked for safety violations.
//
// If the checker is nil, [NewWithChecker] is identical to [New].
func NewWithChecker(lg *slog.Logger, g llm.ContentGenerator, checker llm.PolicyChecker, db storage.DB) *Client {
	return &Client{slog: lg, g: g, checker: checker, db: db}
}

// evaluatePolicy invokes the policy checker on the given prompts and LLM output and
// wraps its results as a [*PolicyEvaluation].
// TODO(tatianabradley): Cache calls to policy checker.
func (c *Client) evaluatePolicy(ctx context.Context, prompts []llm.Part, output string) *PolicyEvaluation {
	if c.checker == nil {
		return nil
	}
	pe := &PolicyEvaluation{}
	for _, p := range prompts {
		switch v := p.(type) {
		case llm.Text:
			r := c.check(ctx, string(v), nil)
			if len(r.Violations) > 0 {
				pe.Violative = true
			}
			pe.PromptResults = append(pe.PromptResults, r)
		default:
			// Other types are not supported for checks yet.
			err := fmt.Errorf("llmapp: can't check policy for prompt part (unsupported type %T", v)
			pe.PromptResults = append(pe.PromptResults, &PolicyResult{Text: "unknown", Error: err})
		}
	}
	r := c.check(ctx, output, prompts)
	if len(r.Violations) > 0 {
		pe.Violative = true
	}
	pe.OutputResults = r
	return pe
}

// check invokes the policy checker on the given text (with optional prompts)
// and returns its results.
func (c *Client) check(ctx context.Context, text string, prompts []llm.Part) *PolicyResult {
	prs, err := c.checker.CheckText(ctx, text, prompts...)
	if err != nil {
		return &PolicyResult{Text: text, Error: fmt.Errorf("llmapp: error while checking for policy violations: %w", err)}
	}
	c.slog.Info("llmapp: found policy results", "text", text, "prompts", prompts, "results", toStrings(prs))
	if vs := violations(prs); len(vs) > 0 {
		c.slog.Warn("llmapp: found policy violations for LLM output", "text", text, "prompts", prompts, "violations", toStrings(vs))
		return &PolicyResult{Text: text, Results: prs, Violations: vs}
	}
	return &PolicyResult{Text: text, Results: prs}
}

func toStrings(prs []*llm.PolicyResult) []string {
	var ss []string
	for _, pr := range prs {
		ss = append(ss, pr.String())
	}
	return ss
}

// violations returns the policies in prs that are in violation.
func violations(prs []*llm.PolicyResult) []*llm.PolicyResult {
	var vs []*llm.PolicyResult
	for _, pr := range prs {
		if pr.IsViolative() {
			vs = append(vs, pr)
		}
	}
	return vs
}

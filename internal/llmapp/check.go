// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llmapp

import (
	"context"
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

// hasPolicyViolation invokes the policy checker on the given prompts and LLM output and
// logs its results. It reports whether any policy violations were found.
// TODO(tatianabradley): Cache calls to policy checker.
func (c *Client) hasPolicyViolation(ctx context.Context, prompts []llm.Part, output string) bool {
	if c.checker == nil {
		return false
	}
	foundViolation := false
	for _, p := range prompts {
		switch v := p.(type) {
		case llm.Text:
			if c.logCheck(ctx, string(v), nil) {
				foundViolation = true
			}
		default:
			// Other types are not supported for checks yet.
			c.slog.Info("llmapp: can't check policy for prompt part (unsupported type)", "prompt part", v)
		}
	}
	if c.logCheck(ctx, output, prompts) {
		return true
	}
	return foundViolation
}

// logCheck invokes the policy checker on the give text (with optional prompts)
// and logs its results.
// It reports whether any policy violations were found.
func (c *Client) logCheck(ctx context.Context, text string, prompts []llm.Part) bool {
	prs, err := c.checker.CheckText(ctx, text, prompts...)
	if err != nil {
		c.slog.Error("llmapp: error checking for policy violations", "err", err)
		return false
	}
	c.slog.Info("llmapp: found policy results", "text", text, "prompts", prompts, "results", toStrings(prs))
	if vs := violations(prs); len(vs) > 0 {
		c.slog.Warn("llmapp: found policy violations for LLM output", "text", text, "prompts", prompts, "violations", toStrings(vs))
		return true
	}
	return false
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

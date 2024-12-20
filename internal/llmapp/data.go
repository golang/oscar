// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llmapp

import (
	"fmt"
	"strings"

	"golang.org/x/oscar/internal/llm"
)

// A Doc is a document to provide to an LLM as part of a prompt.
type Doc struct {
	// Freeform text describing the type of document.
	// Used to help the LLM distinguish between kinds of
	// documents, or understand their relative importance.
	Type string `json:"type,omitempty"`
	// The URL of the document, if one exists.
	URL string `json:"url,omitempty"`
	// The author of the document, if known.
	Author string `json:"author,omitempty"`
	// The title of the document, if known.
	Title string `json:"title,omitempty"`
	Text  string `json:"text"` // required
}

// Result is the result of an LLM call.
type Result struct {
	Response         string            // the raw LLM-generated response
	Cached           bool              // whether the response was cached
	Schema           *llm.Schema       // the JSON schema used to generate the result (nil if none)
	Prompt           []llm.Part        // the prompt(s) used to generate the result
	PolicyEvaluation *PolicyEvaluation // (if a policy checker is configured) the policy evaluation result
}

// A PolicyEvaluation is the result of evaluating a policy against
// a multi-part prompt and an output of an LLM.
type PolicyEvaluation struct {
	Violative     bool // whether any violations were found
	PromptResults []*PolicyResult
	OutputResults *PolicyResult
}

// String returns a human readable representation of a policy evaluation.
func (pe *PolicyEvaluation) String() string {
	if pe == nil {
		return ""
	}
	b := strings.Builder{}
	b.WriteString(fmt.Sprintf("Violative: %t\n\n", pe.Violative))
	b.WriteString("Prompt Results:\n")
	for _, pr := range pe.PromptResults {
		b.WriteString(pr.String() + "\n")
	}
	b.WriteString("Output Results:\n")
	b.WriteString(pe.OutputResults.String() + "\n")
	return b.String()
}

// A PolicyResult is the result of evaluating a policy against
// an input or output to an LLM.
type PolicyResult struct {
	Text       string // the text that was analyzed
	Results    []*llm.PolicyResult
	Violations []*llm.PolicyResult
	Error      error
}

// String returns a human readable representation of a policy result.
func (pr *PolicyResult) String() string {
	if pr == nil {
		return ""
	}
	b := strings.Builder{}
	b.WriteString(fmt.Sprintf("Text: %s\n", pr.Text))
	b.WriteString(fmt.Sprintf("Results: %v\n", pr.Results))
	if len(pr.Violations) > 0 {
		b.WriteString(fmt.Sprintf("Violations: %v\n", pr.Violations))
	}
	if pr.Error != nil {
		b.WriteString(fmt.Sprintf("Error: %v\n", pr.Error))
	}
	return b.String()
}

// HasPolicyViolation reports whether the result or its prompts
// have any policy violations.
func (r *Result) HasPolicyViolation() bool {
	return r.PolicyEvaluation != nil && r.PolicyEvaluation.Violative
}

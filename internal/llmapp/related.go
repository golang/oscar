// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llmapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
)

// RelatedAnalysis is the output of [AnalyzeRelated].
type RelatedAnalysis struct {
	Result
	// The LLM's response, unmarshaled into a Go struct.
	Output Related
}

// Related represents the desired JSON structure of the LLM output
// requested by [AnalyzeRelated].
// See [relatedSchema] for a description of the fields.
//
// IMPORTANT: If you add, remove or edit the types or JSON names of
// fields in this struct, edit [relatedSchema] and
// [relatedTestOutput] accordingly.
type Related struct {
	Summary string       `json:"original_summary"`
	Related []RelatedDoc `json:"related"`
}

// RelatedDoc represents the desired JSON structure of the
// LLM output for a single related document.
type RelatedDoc struct {
	Title           string `json:"title"`
	URL             string `json:"url"`
	Summary         string `json:"summary"`
	Relationship    string `json:"relationship"`
	Relevance       string `json:"relevance"`
	RelevanceReason string `json:"relevance_reason"`
}

// The [*llm.Schema] corresponding to the [Related] type.
//
// IMPORTANT: If you add, remove, or edit the names or types of objects
// in this schema, edit [Related] and [relatedTestOutput] accordingly.
var relatedSchema = &llm.Schema{
	Type: llm.TypeObject,
	Properties: map[string]*llm.Schema{
		"original_summary": {
			Type: llm.TypeString,
		},
		"related": {
			Type: llm.TypeArray,
			Items: &llm.Schema{
				Type: llm.TypeObject,
				Properties: map[string]*llm.Schema{
					"title": {
						Type:        llm.TypeString,
						Description: "The title of the document.",
					},
					"url": {
						Type:        llm.TypeString,
						Description: "The URL of the document.",
					},
					"summary": {
						Type:        llm.TypeString,
						Description: "Summarize the document in one sentence.",
					},
					"relationship": {
						Type:        llm.TypeString,
						Description: "Explain how the document is related to the original document.",
					},
					"relevance": {
						Type: llm.TypeString,
						Enum: []string{
							"HIGH",
							"MEDIUM",
							"LOW",
							"NONE",
						},
						Description: "How relevant is the document?",
					},
					"relevance_reason": {
						Type:        llm.TypeString,
						Description: "Explain reasoning for relevance score.",
					},
				},
				Required: []string{"title", "url", "summary", "relationship", "relevance", "relevance_reason"},
			},
		},
	},
	Required: []string{"original_summary", "related"},
}

// AnalyzeRelated returns a structured, LLM-generated analysis of the given
// document and related documents.
// AnalyzeRelated returns an error if no initial document is provided, no related docs are
// provided, or the LLM is unable to generate a response.
// TODO(tatianabradley): Return an error if the LLM generates JSON with missing or unexpected elements.
func (c *Client) AnalyzeRelated(ctx context.Context, doc *Doc, related []*Doc) (*RelatedAnalysis, error) {
	if doc == nil {
		return nil, errors.New("llmapp AnalyzeRelated: no doc")
	}
	if len(related) == 0 {
		return nil, errors.New("llmapp AnalyzeRelated: no related docs")
	}
	result, err := c.overview(ctx, docAndRelated,
		&docGroup{label: "original", docs: []*Doc{doc}},
		&docGroup{label: "related", docs: related},
	)
	if err != nil {
		return nil, fmt.Errorf("llmapp AnalyzeRelated: cannot generate response: %w", err)
	}
	var typed Related
	if err := json.Unmarshal([]byte(result.Response), &typed); err != nil {
		return nil, fmt.Errorf("llmapp AnalyzeRelated: cannot unmarshal response: %w\nresponse: %s", err, result.Response)
	}
	if len(typed.Related) != len(related) {
		return nil, fmt.Errorf("llmapp AnalyzeRelated: malformed LLM output (unexpected number of related docs: want %d, got %d)", len(related), len(typed.Related))
	}
	return &RelatedAnalysis{Result: *result, Output: typed}, nil
}

// RelatedTestGenerator returns an [llm.ContentGenerator] that can be used
// in tests of the [Client.AnalyzeRelated] method.
// numRelated is the number of related documents that will be provided to the
// [Client.AnalyzeRelated] call.
//
// For testing.
func RelatedTestGenerator(t *testing.T, numRelated int) llm.ContentGenerator {
	t.Helper()

	raw, _ := relatedTestOutput(t, numRelated)
	return llm.TestContentGenerator(
		"related-test-generator",
		func(context.Context, *llm.Schema, []llm.Part) (string, error) {
			return raw, nil
		},
	)
}

// relatedTestOutput returns a JSON string (and its corresponding [Related] struct) that
// would be considered valid if output by the LLM call in [AnalyzeRelated].
// numRelated is the number of related documents that will be provided to the
// [AnalyzeRelated] call.
//
// For testing.
func relatedTestOutput(t *testing.T, numRelated int) (raw string, typed Related) {
	t.Helper()

	var rd = make([]RelatedDoc, numRelated)
	for i := range numRelated {
		rd[i] = RelatedDoc{
			Title:           "title",
			URL:             "URL",
			Summary:         "related summary",
			Relationship:    "related relationship",
			Relevance:       "related relevance",
			RelevanceReason: "related relevance reason",
		}
	}
	r := Related{
		Summary: "original summary",
		Related: rd,
	}
	return string(storage.JSON(r)), r
}

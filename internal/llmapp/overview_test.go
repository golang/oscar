// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llmapp

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/oscar/internal/llm"
)

func TestOverview(t *testing.T) {
	ctx := context.Background()
	g := llm.EchoTextGenerator()
	d1 := &Doc{URL: "https://example.com", Author: "rsc", Title: "title", Text: "some text"}
	d2 := &Doc{Text: "some text 2"}
	got, err := Overview(ctx, g, d1, d2)
	if err != nil {
		t.Fatal(err)
	}
	promptParts := []string{
		`{"url":"https://example.com","author":"rsc","title":"title","text":"some text"}`,
		`{"text":"some text 2"}`,
		documents.instructions(),
	}
	want := &OverviewResult{
		Overview: llm.EchoResponse(promptParts...),
		Prompt:   promptParts,
	}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("Overview() mismatch (-got +want):\n%s", diff)
	}
}

func TestPostOverview(t *testing.T) {
	ctx := context.Background()
	g := llm.EchoTextGenerator()
	d1 := &Doc{URL: "https://example.com", Author: "rsc", Title: "title", Text: "some text"}
	d2 := &Doc{Text: "some text 2"}
	got, err := PostOverview(ctx, g, d1, []*Doc{d2})
	if err != nil {
		t.Fatal(err)
	}
	promptParts := []string{
		`{"url":"https://example.com","author":"rsc","title":"title","text":"some text"}`,
		`{"text":"some text 2"}`,
		postAndComments.instructions(),
	}
	want := &OverviewResult{
		Overview: llm.EchoResponse(promptParts...),
		Prompt:   promptParts,
	}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("PostOverview() mismatch (-got +want):\n%s", diff)
	}
}

func TestInstructions(t *testing.T) {
	wantAll := "markdown" // in all instructions
	wantPost := "post"    // only in PostAndComments

	t.Run("Documents", func(t *testing.T) {
		di := documents.instructions()
		if !strings.Contains(di, wantAll) {
			t.Errorf("Documents.instructions(): does not contain %q", wantAll)
		}
		if strings.Contains(di, wantPost) {
			t.Errorf("Documents.instructions(): incorrectly contains %q", wantPost)
		}
	})

	t.Run("PostAndComments", func(t *testing.T) {
		pi := postAndComments.instructions()
		if !strings.Contains(pi, wantAll) {
			t.Fatalf("PostAndComments.instructions(): does not contain %q", wantAll)
		}
		if !strings.Contains(pi, wantPost) {
			t.Fatalf("PostAndComments.instructions(): does not contain %q", wantPost)
		}
	})
}

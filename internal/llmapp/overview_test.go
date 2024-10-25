// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llmapp

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/oscar/internal/llm"
)

func TestOverview(t *testing.T) {
	ctx := context.Background()
	g := llm.EchoTextGenerator()
	d1 := &Doc{URL: "https://example.com", Author: "rsc", Title: "title", Text: "some text"}
	d2 := &Doc{Text: "some text 2"}
	got, err := Overview(ctx, g, Documents, d1, d2)
	if err != nil {
		t.Fatal(err)
	}
	prompt := Documents.instructions()
	want := llm.EchoResponse(
		`{"url":"https://example.com","author":"rsc","title":"title","text":"some text"}`,
		`{"text":"some text 2"}`,
		prompt)
	if got != want {
		t.Errorf("Overview() = %s, want %s", got, want)
	}
	t.Log(want)
}

func TestInstructions(t *testing.T) {
	wantAll := "markdown" // in all instructions
	wantPost := "post"    // only in PostAndComments

	t.Run("Documents", func(t *testing.T) {
		di := Documents.instructions()
		if !strings.Contains(di, wantAll) {
			t.Errorf("Documents.instructions(): does not contain %q", wantAll)
		}
		if strings.Contains(di, wantPost) {
			t.Errorf("Documents.instructions(): incorrectly contains %q", wantPost)
		}
	})

	t.Run("PostAndComments", func(t *testing.T) {
		pi := PostAndComments.instructions()
		if !strings.Contains(pi, wantAll) {
			t.Fatalf("PostAndComments.instructions(): does not contain %q", wantAll)
		}
		if !strings.Contains(pi, wantPost) {
			t.Fatalf("PostAndComments.instructions(): does not contain %q", wantPost)
		}
	})
}

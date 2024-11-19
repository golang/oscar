// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llmapp

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestOverview(t *testing.T) {
	ctx := context.Background()
	c := newTestClient(t)

	t.Run("Overview", func(t *testing.T) {
		got, err := c.Overview(ctx, doc1, doc2)
		if err != nil {
			t.Fatal(err)
		}
		promptParts := []string{raw1, raw2, documents.instructions()}
		want := &OverviewResult{
			Overview: llm.EchoResponse(promptParts...),
			Prompt:   promptParts,
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Overview() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("PostOverview", func(t *testing.T) {
		got, err := c.PostOverview(ctx, doc1, []*Doc{doc2})
		if err != nil {
			t.Fatal(err)
		}
		promptParts := []string{raw1, raw2, postAndComments.instructions()}
		want := &OverviewResult{
			Overview: llm.EchoResponse(promptParts...),
			Prompt:   promptParts,
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("PostOverview() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("RelatedOverview", func(t *testing.T) {
		got, err := c.RelatedOverview(ctx, doc1, []*Doc{doc2})
		if err != nil {
			t.Fatal(err)
		}
		promptParts := []string{raw1, raw2, docAndRelated.instructions()}
		want := &OverviewResult{
			Overview: llm.EchoResponse(promptParts...),
			Prompt:   promptParts,
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("RelatedOverview() mismatch (-want +got):\n%s", diff)
		}
	})
}

var (
	doc1 = &Doc{URL: "https://example.com", Author: "rsc", Title: "title", Text: "some text"}
	doc2 = &Doc{Text: "some text 2"}
	raw1 = `{"url":"https://example.com","author":"rsc","title":"title","text":"some text"}`
	raw2 = `{"text":"some text 2"}`
)

func newTestClient(t *testing.T) *Client {
	t.Helper()
	return New(testutil.Slogger(t), llm.EchoTextGenerator(), storage.MemDB())
}

func TestGenerateText(t *testing.T) {
	ctx := context.Background()

	lg := testutil.Slogger(t)
	db := storage.MemDB()

	t.Run("echo", func(t *testing.T) {
		c := New(lg, llm.EchoTextGenerator(), db)
		got, cached, err := c.generateText(ctx, []string{"a", "b", "c"})
		if err != nil {
			t.Fatal(err)
		}
		want := llm.EchoResponse("a", "b", "c")
		if got != want {
			t.Errorf("generateText() = %q, want %q", got, want)
		}
		if cached {
			t.Error("generateText() = cached, want not cached")
		}

		// The result should be cached on the second call.
		got, cached, err = c.generateText(ctx, []string{"a", "b", "c"})
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("generateText() = %q, want %q", got, want)
		}
		if !cached {
			t.Error("generateText() = not cached, want cached")
		}
	})

	// Test with a non-deterministic text generator to ensure
	// caching actually works.
	t.Run("random", func(t *testing.T) {
		c := New(lg, random{}, db)
		got1, cached, err := c.generateText(ctx, []string{"a", "b", "c"})
		if err != nil {
			t.Fatal(err)
		}
		if cached {
			t.Error("generateText() = cached, want not cached")
		}

		got2, cached, err := c.generateText(ctx, []string{"a", "b", "c"})
		if err != nil {
			t.Fatal(err)
		}
		if got2 != got1 {
			t.Errorf("generateText() = %s, want %s", got2, got1)
		}
		if !cached {
			t.Error("generateText() = not cached, want cached")
		}
	})
}

// random is an [llm.TextGenerator] that ignores its prompt and
// returns a random integer.
type random struct{}

func (random) Model() string {
	return "random"
}

func (random) GenerateText(_ context.Context, s ...string) (string, error) {
	return strconv.Itoa(rand.IntN(1000)), nil
}

func TestResponseUnmarshal(t *testing.T) {
	// Do not remove or edit this test case without a good reason.
	// It ensures that no backwards incompatible changes are made to the [response] struct.
	raw := `{"Model":"model","PromptHash":"Qb+qD8ZuYR26qktIqPIbbHTaWm0SaoBaWhwObKH8INg=","Response":"response"}`
	var r response
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	if r.Response != "response" {
		t.Errorf("r.Response = %s, want %s", r.Response, "response")
	}
}

func TestInstructions(t *testing.T) {
	wantAll := "markdown"    // in all instructions
	wantPost := "post"       // only in postAndComments
	wantRelated := "related" // only in docAndRelated

	t.Run("documents", func(t *testing.T) {
		di := documents.instructions()
		if !strings.Contains(di, wantAll) {
			t.Errorf("documents.instructions(): does not contain %q", wantAll)
		}
		if strings.Contains(di, wantPost) {
			t.Errorf("documents.instructions(): incorrectly contains %q", wantPost)
		}
	})

	t.Run("postAndComments", func(t *testing.T) {
		pi := postAndComments.instructions()
		if !strings.Contains(pi, wantAll) {
			t.Fatalf("postAndComments.instructions(): does not contain %q", wantAll)
		}
		if !strings.Contains(pi, wantPost) {
			t.Fatalf("postAndComments.instructions(): does not contain %q", wantPost)
		}
	})

	t.Run("DocAndRelated", func(t *testing.T) {
		pi := docAndRelated.instructions()
		if !strings.Contains(pi, wantAll) {
			t.Fatalf("docAndRelated.instructions(): does not contain %q", wantAll)
		}
		if !strings.Contains(pi, wantRelated) {
			t.Fatalf("docAndRelated.instructions(): does not contain %q", wantPost)
		}
	})
}

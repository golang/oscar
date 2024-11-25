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
		promptParts := []any{raw1, raw2, documents.instructions()}
		want := &Result{
			Response: llm.EchoTextResponse(promptParts...),
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
		promptParts := []any{"post", raw1, "comments", raw2, postAndComments.instructions()}
		want := &Result{
			Response: llm.EchoTextResponse(promptParts...),
			Prompt:   promptParts,
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("PostOverview() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("UpdatedPostOverview", func(t *testing.T) {
		got, err := c.UpdatedPostOverview(ctx, doc1, []*Doc{doc2}, []*Doc{doc3})
		if err != nil {
			t.Fatal(err)
		}
		promptParts := []any{"post", raw1, "old comments", raw2, "new comments", raw3, postAndCommentsUpdated.instructions()}
		want := &Result{
			Response: llm.EchoTextResponse(promptParts...),
			Prompt:   promptParts,
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("UpdatedPostOverview() mismatch (-want +got):\n%s", diff)
		}
	})
}

var (
	doc1 = &Doc{URL: "https://example.com", Author: "rsc", Title: "title", Text: "some text"}
	doc2 = &Doc{Text: "some text 2"}
	doc3 = &Doc{Text: "some text 3"}
	raw1 = `{"url":"https://example.com","author":"rsc","title":"title","text":"some text"}`
	raw2 = `{"text":"some text 2"}`
	raw3 = `{"text":"some text 3"}`
)

func newTestClient(t *testing.T) *Client {
	t.Helper()
	return New(testutil.Slogger(t), llm.EchoContentGenerator(), storage.MemDB())
}

func TestGenerate(t *testing.T) {
	ctx := context.Background()

	lg := testutil.Slogger(t)
	db := storage.MemDB()

	t.Run("echo", func(t *testing.T) {
		c := New(lg, llm.EchoContentGenerator(), db)
		got, cached, err := c.generate(ctx, nil, []any{"a", "b", "c"})
		if err != nil {
			t.Fatal(err)
		}
		want := llm.EchoTextResponse("a", "b", "c")
		if got != want {
			t.Errorf("generate() = %q, want %q", got, want)
		}
		if cached {
			t.Error("generate() = cached, want not cached")
		}

		// The result should be cached on the second call.
		got, cached, err = c.generate(ctx, nil, []any{"a", "b", "c"})
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("generate() = %q, want %q", got, want)
		}
		if !cached {
			t.Error("generate() = not cached, want cached")
		}
	})

	// Test with a non-deterministic text generator to ensure
	// caching actually works.
	t.Run("random", func(t *testing.T) {
		c := New(lg, randomContentGenerator(), db)
		got1, cached, err := c.generate(ctx, nil, []any{"a", "b", "c"})
		if err != nil {
			t.Fatal(err)
		}
		if cached {
			t.Error("generate() = cached, want not cached")
		}

		got2, cached, err := c.generate(ctx, nil, []any{"a", "b", "c"})
		if err != nil {
			t.Fatal(err)
		}
		if got2 != got1 {
			t.Errorf("generate() = %s, want %s", got2, got1)
		}
		if !cached {
			t.Error("generate() = not cached, want cached")
		}
	})
}

// randomContentGenerator returns an [llm.ContentGenerator] that ignores
// its prompt and returns a random integer.
func randomContentGenerator() llm.ContentGenerator {
	return llm.TestContentGenerator(
		"random",
		func(_ context.Context, s *llm.Schema, _ []any) (string, error) {
			n := strconv.Itoa(rand.IntN(1000))
			if s != nil {
				return `{"value":` + n + "}", nil
			}
			return n, nil
		},
	)
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
	markdown := "markdown"   // in all text instructions
	wantPost := "post"       // only in postAndComments
	wantRelated := "related" // only in docAndRelated

	t.Run("documents", func(t *testing.T) {
		di := documents.instructions()
		if !strings.Contains(di, markdown) {
			t.Errorf("documents.instructions(): does not contain %q", markdown)
		}
		if strings.Contains(di, wantPost) {
			t.Errorf("documents.instructions(): incorrectly contains %q", wantPost)
		}
	})

	t.Run("postAndComments", func(t *testing.T) {
		pi := postAndComments.instructions()
		if !strings.Contains(pi, markdown) {
			t.Fatalf("postAndComments.instructions(): does not contain %q", markdown)
		}
		if !strings.Contains(pi, wantPost) {
			t.Fatalf("postAndComments.instructions(): does not contain %q", wantPost)
		}
	})

	t.Run("docAndRelated", func(t *testing.T) {
		pi := docAndRelated.instructions()
		// not markdown
		if !strings.Contains(pi, wantRelated) {
			t.Fatalf("docAndRelated.instructions(): does not contain %q", wantRelated)
		}
	})
}

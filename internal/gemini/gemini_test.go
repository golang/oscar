// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gemini

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"

	"golang.org/x/oscar/internal/httprr"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/testutil"
)

var docs = []llm.EmbedDoc{
	{Text: "for loops"},
	{Text: "for all time, always"},
	{Text: "break statements"},
	{Text: "breakdancing"},
	{Text: "forever could never be long enough for me"},
	{Text: "the macarena"},
}

var matches = map[string]string{
	"for loops":            "break statements",
	"for all time, always": "forever could never be long enough for me",
	"breakdancing":         "the macarena",
}

func init() {
	for k, v := range matches {
		matches[v] = k
	}
}

var ctx = context.Background()

func newTestClient(t *testing.T, rrfile string) *Client {
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)

	rr, err := httprr.Open(rrfile, http.DefaultTransport)
	check(err)
	rr.Scrub(Scrub)
	sdb := secret.ReadOnlyMap{"ai.google.dev": "nokey"}

	c, err := NewClient(ctx, lg, sdb, rr.Client(), "text-embedding-004")
	check(err)

	return c
}

func TestEmbedBatch(t *testing.T) {
	ctx := context.Background()
	check := testutil.Checker(t)
	c := newTestClient(t, "testdata/embedbatch.httprr")
	vecs, err := c.EmbedDocs(ctx, docs)
	check(err)
	if len(vecs) != len(docs) {
		t.Fatalf("len(vecs) = %d, but len(docs) = %d", len(vecs), len(docs))
	}

	var buf bytes.Buffer
	for i := range docs {
		for j := range docs {
			fmt.Fprintf(&buf, " %.4f", vecs[i].Dot(vecs[j]))
		}
		fmt.Fprintf(&buf, "\n")
	}

	for i, d := range docs {
		best := ""
		bestDot := 0.0
		for j := range docs {
			if dot := vecs[i].Dot(vecs[j]); i != j && dot > bestDot {
				best, bestDot = docs[j].Text, dot
			}
		}
		if best != matches[d.Text] {
			if buf.Len() > 0 {
				t.Errorf("dot matrix:\n%s", buf.String())
				buf.Reset()
			}
			t.Errorf("%q: best=%q, want %q", d.Text, best, matches[d.Text])
		}
	}
}

func TestBigBatch(t *testing.T) {
	ctx := context.Background()
	check := testutil.Checker(t)
	c := newTestClient(t, "testdata/bigbatch.httprr")
	var docs []llm.EmbedDoc

	for i := range 251 {
		docs = append(docs, llm.EmbedDoc{Text: fmt.Sprintf("word%d", i)})
	}
	docs = docs[:251]
	vecs, err := c.EmbedDocs(ctx, docs)
	check(err)
	if len(vecs) != len(docs) {
		t.Fatalf("len(vecs) = %d, but len(docs) = %d", len(vecs), len(docs))
	}
}

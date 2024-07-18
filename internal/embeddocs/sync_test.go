// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package embeddocs

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

var texts = []string{
	"for loops",
	"for all time, always",
	"break statements",
	"breakdancing",
	"forever could never be long enough for me",
	"the macarena",
}

func checker(t *testing.T) func(error) {
	return func(err error) {
		if err != nil {
			t.Helper()
			t.Fatal(err)
		}
	}
}

var ctx = context.Background()

func TestSync(t *testing.T) {
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	vdb := storage.MemVectorDB(db, lg, "step1")
	dc := docs.New(db)
	for i, text := range texts {
		dc.Add(fmt.Sprintf("URL%d", i), "", text)
	}

	Sync(ctx, lg, vdb, llm.QuoteEmbedder(), dc)
	for i, text := range texts {
		vec, ok := vdb.Get(fmt.Sprintf("URL%d", i))
		if !ok {
			t.Errorf("URL%d missing from vdb", i)
			continue
		}
		vtext := llm.UnquoteVector(vec)
		if vtext != text {
			t.Errorf("URL%d decoded to %q, want %q", i, vtext, text)
		}
	}

	for i, text := range texts {
		dc.Add(fmt.Sprintf("rot13%d", i), "", rot13(text))
	}
	vdb2 := storage.MemVectorDB(db, lg, "step2")
	Sync(ctx, lg, vdb2, llm.QuoteEmbedder(), dc)
	for i, text := range texts {
		vec, ok := vdb2.Get(fmt.Sprintf("URL%d", i))
		if ok {
			t.Errorf("URL%d written during second sync: %q", i, llm.UnquoteVector(vec))
			continue
		}

		vec, ok = vdb2.Get(fmt.Sprintf("rot13%d", i))
		vtext := llm.UnquoteVector(vec)
		if vtext != rot13(text) {
			t.Errorf("rot13%d decoded to %q, want %q", i, vtext, rot13(text))
		}
	}
}

func TestBigSync(t *testing.T) {
	const N = 10000

	lg := testutil.Slogger(t)
	db := storage.MemDB()
	vdb := storage.MemVectorDB(db, lg, "vdb")
	dc := docs.New(db)
	for i := range N {
		dc.Add(fmt.Sprintf("URL%d", i), "", fmt.Sprintf("Text%d", i))
	}

	Sync(ctx, lg, vdb, llm.QuoteEmbedder(), dc)
	for i := range N {
		vec, ok := vdb.Get(fmt.Sprintf("URL%d", i))
		if !ok {
			t.Errorf("URL%d missing from vdb", i)
			continue
		}
		text := fmt.Sprintf("Text%d", i)
		vtext := llm.UnquoteVector(vec)
		if vtext != text {
			t.Errorf("URL%d decoded to %q, want %q", i, vtext, text)
		}
	}
}

func TestBadEmbedders(t *testing.T) {
	const N = 150
	db := storage.MemDB()
	dc := docs.New(db)
	for i := range N {
		dc.Add(fmt.Sprintf("URL%03d", i), "", fmt.Sprintf("Text%d", i))
	}

	lg, out := testutil.SlogBuffer()
	db = storage.MemDB()
	vdb := storage.MemVectorDB(db, lg, "vdb")
	Sync(ctx, lg, vdb, tooManyEmbed{}, dc)
	if !strings.Contains(out.String(), "embeddocs length mismatch") {
		t.Errorf("tooManyEmbed did not report error:\n%s", out)
	}

	lg, out = testutil.SlogBuffer()
	db = storage.MemDB()
	vdb = storage.MemVectorDB(db, lg, "vdb")
	Sync(ctx, lg, vdb, embedErr{}, dc)
	if !strings.Contains(out.String(), "EMBED ERROR") {
		t.Errorf("embedErr did not report error:\n%s", out)
	}
	if _, ok := vdb.Get("URL001"); !ok {
		t.Errorf("Sync did not write URL001 after embedErr")
	}

	lg, out = testutil.SlogBuffer()
	db = storage.MemDB()
	vdb = storage.MemVectorDB(db, lg, "vdb")
	Sync(ctx, lg, vdb, embedHalf{}, dc)
	if !strings.Contains(out.String(), "length mismatch") {
		t.Errorf("embedHalf did not report error:\n%s", out)
	}
	if _, ok := vdb.Get("URL001"); !ok {
		t.Errorf("Sync did not write URL001 after embedHalf")
	}
}

func rot13(s string) string {
	b := []byte(s)
	for i, x := range b {
		if 'A' <= x && x <= 'M' || 'a' <= x && x <= 'm' {
			b[i] = x + 13
		} else if 'N' <= x && x <= 'Z' || 'n' <= x && x <= 'z' {
			b[i] = x - 13
		}
	}
	return string(b)
}

type tooManyEmbed struct{}

func (tooManyEmbed) EmbedDocs(ctx context.Context, docs []llm.EmbedDoc) ([]llm.Vector, error) {
	vec, _ := llm.QuoteEmbedder().EmbedDocs(ctx, docs)
	vec = append(vec, vec...)
	return vec, nil
}

type embedErr struct{}

func (embedErr) EmbedDocs(ctx context.Context, docs []llm.EmbedDoc) ([]llm.Vector, error) {
	vec, _ := llm.QuoteEmbedder().EmbedDocs(ctx, docs)
	return vec, fmt.Errorf("EMBED ERROR")
}

type embedHalf struct{}

func (embedHalf) EmbedDocs(ctx context.Context, docs []llm.EmbedDoc) ([]llm.Vector, error) {
	vec, _ := llm.QuoteEmbedder().EmbedDocs(ctx, docs)
	vec = vec[:len(vec)/2]
	return vec, nil
}

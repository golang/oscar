// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package llm defines interfaces implemented by LLMs (or LLM-related services).
package llm

import (
	"context"
	"encoding/binary"
	"math"
)

// An Embedder computes vector embeddings of a list of documents.
//
// EmbedDocs accepts an arbitrary number of documents and returns
// their embeddings. If the underlying implementation has a limit on
// the batch size, it should make multiple requests in order to process
// all the documents. If an error occurs after some, but not all, documents
// have been processed, EmbedDocs can return an error along with a
// shortened vector slice giving the vectors for a prefix of the document slice.
//
// See [QuoteEmbedder] for a semantically useless embedder that
// can nonetheless be helpful when writing tests,
// and see [golang.org/x/oscar/internal/gcp/gemini] for a real implementation.
type Embedder interface {
	EmbedDocs(ctx context.Context, docs []EmbedDoc) ([]Vector, error)
}

// An EmbedDoc is a single document to be embedded.
type EmbedDoc struct {
	Title string // title of document (optional)
	Text  string // text of document
}

// A Vector is an embedding vector, typically a high-dimensional unit vector.
type Vector []float32

// Dot returns the dot product of v and w.
//
// TODO(rsc): Using a float64 for the result is slightly higher
// precision and may be worth doing in the intermediate calculation
// but may not be worth the type conversions involved to return a float64.
// Perhaps the return type should still be float32 even if the math is float64.
func (v Vector) Dot(w Vector) float64 {
	v = v[:min(len(v), len(w))]
	w = w[:len(v)] // make "i in range for v" imply "i in range for w" to remove bounds check in loop
	t := float64(0)
	for i := range v {
		t += float64(v[i]) * float64(w[i])
	}
	return t
}

// Encode returns a byte encoding of the vector v,
// suitable for storing in a database.
func (v Vector) Encode() []byte {
	val := make([]byte, 4*len(v))
	for i, f := range v {
		binary.BigEndian.PutUint32(val[4*i:], math.Float32bits(f))
	}
	return val
}

// Decode decodes the byte encoding enc into the vector v.
// Enc should be a multiple of 4 bytes; any trailing bytes are ignored.
func (v *Vector) Decode(enc []byte) {
	if len(*v) < len(enc)/4 {
		*v = make(Vector, len(enc)/4)
	}
	*v = (*v)[:0]
	for ; len(enc) >= 4; enc = enc[4:] {
		*v = append(*v, math.Float32frombits(binary.BigEndian.Uint32(enc)))
	}
}

// A TextGenerator generates text responses given a text prompt.
//
// See [EchoTextGenerator] for a generator, useful for testing, that
// always responds with a deterministic message derived from the prompt.
//
// See [golang.org/x/oscar/internal/gcp/gemini] for a real implementation.
type TextGenerator interface {
	GenerateText(ctx context.Context, prompt string) ([]string, error)
}

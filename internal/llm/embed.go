// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llm

import (
	"context"
	"math"
)

const quoteLen = 123

// QuoteEmbedder returns an implementation
// of Embedder that can be useful for testing but
// is completely pointless for real use.
// It encodes up to the first 122 bytes of each document
// directly into the first 122 elements of a 123-element unit vector.
func QuoteEmbedder() Embedder {
	return quoter{}
}

// quote quotes text into a vector.
// The text ends at the first negative entry in the vector.
// The final entry of the vector is hard-coded to -1
// before normalization, so that the final entry of a
// normalized vector lets us know scaling to reverse
// to obtain the original bytes.
func quote(text string) Vector {
	v := make(Vector, quoteLen)
	var d float64
	for i := range len(text) {
		if i >= len(v)-1 {
			break
		}
		v[i] = float32(byte(text[i])) / 256
		d += float64(v[i]) * float64(v[i])
	}
	if len(text)+1 < len(v) {
		v[len(text)] = -1
		d += 1
	}
	v[len(v)-1] = -1
	d += 1

	d = 1 / math.Sqrt(d)
	for i := range v {
		v[i] *= float32(d)
	}
	return v
}

// quoter is a quoting Embedder, returned by QuoteEmbedder
type quoter struct{}

// EmbedDocs implements Embedder by quoting.
func (quoter) EmbedDocs(ctx context.Context, docs []EmbedDoc) ([]Vector, error) {
	var vecs []Vector
	for _, d := range docs {
		vecs = append(vecs, quote(d.Text))
	}
	return vecs, nil
}

// UnquoteVector recovers the original text prefix
// passed to a [QuoteEmbedder]'s EmbedDocs method.
// Like QuoteEmbedder, UnquoteVector is only useful in tests.
func UnquoteVector(v Vector) string {
	if len(v) != quoteLen {
		panic("UnquoteVector of non-quotation vector")
	}
	d := -1 / v[len(v)-1]
	var b []byte
	for _, f := range v {
		if f < 0 {
			break
		}
		b = append(b, byte(256*f*d+0.5))
	}
	return string(b)
}

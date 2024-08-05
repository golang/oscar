// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package storage

import (
	"math"
	"reflect"
	"slices"
	"testing"

	"golang.org/x/oscar/internal/llm"
)

func TestVectorDB(t *testing.T, newdb func() VectorDB) {
	vdb := newdb()

	vdb.Set("orange2", embed("orange2"))
	vdb.Set("orange1", embed("orange1"))
	vdb.Set("orange1alias", embed("orange1"))
	vdb.Delete("orange1alias")

	b := vdb.Batch()
	b.Set("apple3", embed("apple3"))
	b.Set("apple4", embed("apple4"))
	b.Set("apple4alias", embed("apple4"))
	b.Delete("apple4alias")
	b.Set("ignore", embed("bad")[:4])
	b.Apply()

	v, ok := vdb.Get("apple3")
	if !ok || !slices.Equal(v, embed("apple3")) {
		// unreachable except bad vectordb
		t.Errorf("Get(apple3) = %v, %v, want %v, true", v, ok, embed("apple3"))
	}

	want := []VectorResult{
		{"apple4", 0.9999961187341375},
		{"apple3", 0.9999843342970269},
		{"orange1", 0.38062230442542155},
		{"orange2", 0.3785152783773009},
	}
	have := vdb.Search(embed("apple5"), 5)
	if !reflect.DeepEqual(have, want) {
		// unreachable except bad vectordb
		t.Fatalf("Search(apple5, 5):\nhave %v\nwant %v", have, want)
	}

	vdb.Flush()

	vdb = newdb()
	have = vdb.Search(embed("apple5"), 3)
	want = want[:3]
	if !reflect.DeepEqual(have, want) {
		// unreachable except bad vectordb
		t.Errorf("Search(apple5, 3) in fresh database:\nhave %v\nwant %v", have, want)
	}
}

func embed(text string) llm.Vector {
	const vectorLen = 16
	v := make(llm.Vector, vectorLen)
	d := float32(0)
	for i := range len(text) {
		v[i] = float32(byte(text[i])) / 256
		d += float32(v[i] * v[i]) // float32() to avoid FMA
	}
	if len(text) < len(v) {
		v[len(text)] = -1
		d += 1
	}
	d = float32(1 / math.Sqrt(float64(d)))
	for i, x := range v {
		v[i] = x * d
	}
	return v
}

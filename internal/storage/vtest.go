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

// TestVectorDB verifies that implementations of [VectorDB]
// conform to its specification.
// The opendb function should create a new connection to the same underlying storage.
func TestVectorDB(t *testing.T, opendb func() VectorDB) {
	vdb := opendb()

	vdb.Set("orange2", embed("orange2"))
	vdb.Set("orange1", embed("orange1"))
	vdb.Set("orange1alias", embed("orange1"))
	vdb.Delete("orange1alias")
	vdb.Delete("orange1")
	vdb.Set("orange1", embed("orange1"))

	haveAll := allIDs(vdb)
	wantAll := []string{"orange1", "orange2"}
	if !reflect.DeepEqual(haveAll, wantAll) {
		t.Fatalf("All(): have %v;\nwant %v", haveAll, wantAll)
	}

	vdb.Set("apple1", embed("apple1"))
	b := vdb.Batch()
	b.Delete("apple1")
	b.Set("apple3", embed("apple3"))
	b.Set("apple4", embed("apple4"))
	b.Set("apple4alias", embed("apple4"))
	b.Delete("apple4alias")
	b.Set("ignore", embed("bad")[:4])
	b.Set("orange3", embed("orange3"))
	b.Delete("orange3")
	b.Delete("orange4")
	b.Set("orange4", embed("orange4"))
	b.Apply()

	haveAll = allIDs(vdb)
	wantAll = []string{"apple3", "apple4", "ignore", "orange1", "orange2", "orange4"}
	if !reflect.DeepEqual(haveAll, wantAll) {
		t.Fatalf("All(): have %v;\nwant %v", haveAll, wantAll)
	}

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
		{"orange4", 0.37429777504303363},
	}
	have := vdb.Search(embed("apple5"), 5)
	if !reflect.DeepEqual(have, want) {
		// unreachable except bad vectordb
		t.Fatalf("Search(apple5, 5):\nhave %v\nwant %v", have, want)
	}

	vdb.Flush()

	vdb = opendb()
	have = vdb.Search(embed("apple5"), 3)
	want = want[:3]
	if !reflect.DeepEqual(have, want) {
		// unreachable except bad vectordb
		t.Errorf("Search(apple5, 3) in fresh database:\nhave %v\nwant %v", have, want)
	}
}

func allIDs(vdb VectorDB) []string {
	var all []string
	for k := range vdb.All() {
		all = append(all, k)
	}
	return all
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

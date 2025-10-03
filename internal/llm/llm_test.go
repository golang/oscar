// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llm

import (
	"math"
	"slices"
	"testing"
)

func TestVector(t *testing.T) {
	v1 := Vector{1, 2, 3, 4}
	v2 := Vector{-200, -3000, 0, -10000}
	dot := v1.Dot(v2)
	if dot != -46200 {
		t.Errorf("%v.Dot(%v) = %v, want -46200", v1, v2, dot)
	}

	enc := v1.Encode()
	var v3 Vector
	v3.Decode(enc)
	if !slices.Equal(v3, v1) {
		t.Errorf("Decode(Encode(%v)) = %v, want %v", v1, v3, v1)
	}

	v1Abs := math.Sqrt(v1.Dot(v1))
	n := v1.Normal()
	if dot := v1.Dot(n); math.Abs(dot-v1Abs) > 1e-5 {
		t.Errorf("v1.Dot(v1) = %v; v1.Dot(v1.Normal()) = %v, want %v", v1Abs, dot, v1Abs)
	}
	if dot := n.Dot(n); math.Abs(dot-1) > 1e-5 {
		t.Errorf("n.Dot(n) = %v, want %v", dot, 1.0)
	}
}

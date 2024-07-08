// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package storage

import "testing"

func TestVectorResultCompare(t *testing.T) {
	type R = VectorResult
	var tests = []struct {
		x, y VectorResult
		cmp  int
	}{
		{R{"b", 0.5}, R{"c", 0.5}, -1},
		{R{"b", 0.4}, R{"a", 0.5}, -1},
	}

	try := func(x, y VectorResult, cmp int) {
		if c := x.cmp(y); c != cmp {
			t.Errorf("Compare(%v, %v) = %d, want %d", x, y, c, cmp)
		}
	}
	for _, tt := range tests {
		try(tt.x, tt.x, 0)
		try(tt.y, tt.y, 0)
		try(tt.x, tt.y, tt.cmp)
		try(tt.y, tt.x, -tt.cmp)
	}
}

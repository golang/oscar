// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package reviews

import (
	"slices"
	"testing"
)

func TestPredicates(t *testing.T) {
	gc := testGerritClient(t)
	const file = "testdata/gerritchange.txt"
	const num = 1
	change := loadTestChange(t, gc, file, 1)
	sc, ok, err := ApplyPredicates(change)
	if err != nil {
		t.Fatalf("%s: %d: ApplyPredicates returned unexpected error %v", file, num, err)
	} else if !ok {
		t.Fatalf("%s: %d: ApplyPredicates unexpectedly rejected change", file, num)
	}
	var got []string
	for _, s := range sc.Predicates {
		got = append(got, s.Name)
	}
	want := []string{"authorReviewer"}
	if !slices.Equal(got, want) {
		t.Errorf("%s: %d: got %v, want %v", file, num, got, want)
	}
}

func TestReject(t *testing.T) {
	gc := testGerritClient(t)
	const file = "testdata/gerritchange.txt"
	const num = 2
	change := loadTestChange(t, gc, file, num)
	_, ok, err := ApplyPredicates(change)
	if err != nil {
		t.Fatalf("%s: %d: ApplyPredicates returned unexpected error %v", file, num, err)
	} else if ok {
		t.Fatalf("%s: %d: ApplyPredicates unexpectedly accepted change", file, num)
	}
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"maps"
	"strconv"
	"testing"

	"golang.org/x/oscar/internal/storage"
	"rsc.io/ordered"
)

func TestSyncDB(t *testing.T) {
	ms := []map[string]int{
		{},
		{"a": 1},
		{"b": 1},
		{"a": 1, "b": 2},
		{"b": 2, "c": 3},
	}
	// Check every pair of maps.
	for _, sm := range ms {
		for _, dm := range ms {
			src := mapToDB(sm)
			// These should not be copied to dst.
			src.Set(ordered.Encode("llm.Vector", "ns", "x"), []byte("0"))
			src.Set(ordered.Encode("llm.Vector", "ns", "y"), []byte("0"))
			dst := mapToDB(dm)
			n := syncDB(dst, src)
			m := dbToMap(t, dst)
			if !maps.Equal(m, sm) {
				t.Errorf("syncDB(dst=%v, src=%v): dst = %v; should equal src", dm, sm, m)
			}
			if want := countDiffs(sm, dm); n != want {
				t.Errorf("synced %d items, wanted %d", n, want)
			}
		}
	}
}

// countDiffs counts the differences in the maps:
// the number of changes to dst that must be made for it to be equal to src.
func countDiffs(src, dst map[string]int) int {
	n := 0
	for k, sv := range src {
		if bytes.HasPrefix([]byte(k), llmVector) {
			continue
		}
		// Need to copy if the key is either missing or has a different value.
		if dv, ok := dst[k]; !ok || dv != sv {
			n++
		}
	}
	for k := range dst {
		if _, ok := src[k]; !ok {
			n++
		}
	}
	return n
}

func TestCountDiffs(t *testing.T) {
	src := map[string]int{"a": 0, "b": 1, "c": 2}
	dst := map[string]int{"b": 1, "c": 3, "d": 4}
	// "a" and "c" are copied, "d" is deleted.
	want := 3
	if got := countDiffs(src, dst); got != want {
		t.Errorf("got %d, want %d", got, want)
	}
}

func mapToDB(m map[string]int) storage.DB {
	db := storage.MemDB()
	for k, v := range m {
		db.Set(ordered.Encode(k), []byte(strconv.Itoa(v)))
	}
	return db
}

func dbToMap(t *testing.T, db storage.DB) map[string]int {
	t.Helper()
	m := map[string]int{}
	for k, vf := range db.Scan(nil, ordered.Encode(ordered.Inf)) {
		var sk string
		if _, err := ordered.DecodePrefix(k, &sk); err != nil {
			t.Fatal(err)
		}
		iv, err := strconv.Atoi(string(vf()))
		if err != nil {
			t.Fatal(err)
		}
		m[sk] = iv
	}
	return m
}

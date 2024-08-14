// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"log/slog"
	"maps"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
	"rsc.io/ordered"
)

var testMaps = []map[string]int{
	{},
	{"a": 1},
	{"b": 1},
	{"a": 1, "b": 2},
	{"b": 2, "c": 3},
	{"p": 4},
}

func TestSyncDB(t *testing.T) {
	// Check every pair of maps.
	for _, sm := range testMaps {
		for _, dm := range testMaps {
			src := mapToDB(sm)
			// These should not be copied to dst.
			src.Set(ordered.Encode("llm.Vector", "ns", "x"), []byte("0"))
			src.Set(ordered.Encode("llm.Vector", "ns", "y"), []byte("0"))
			dst := mapToDB(dm)
			// These should not be deleted from dst.
			dst.Set(ordered.Encode("llm.Vector", "ns", "w"), []byte("0"))
			dst.Set(ordered.Encode("llm.Vector", "ns", "z"), []byte("0"))

			srcNonVec, srcVec := split(t, src)
			dstNonVec, dstVec := split(t, dst)
			syncDB(dst, src)
			srcSyncNonVec, srcSyncVec := split(t, src)
			dstSyncNonVec, dstSyncVec := split(t, dst)

			if !maps.Equal(dstSyncNonVec, srcSyncNonVec) {
				t.Errorf("syncDB(dst=%v, src=%v): dst = %v; should equal src", dstNonVec, srcNonVec, dstSyncNonVec)
			}
			if !maps.Equal(dstVec, dstSyncVec) {
				t.Errorf("vector dst=%v should equal synced vector dst=%v", dstVec, dstSyncVec)
			}
			if !maps.Equal(srcVec, srcSyncVec) {
				t.Errorf("vector src=%v should equal synced vector dst=%v", srcVec, srcSyncVec)
			}
		}
	}
}

func TestSyncVecDB(t *testing.T) {
	// Check every pair of maps.
	for _, sm := range testMaps {
		for _, dm := range testMaps {
			src := storage.MemDB()
			// These should not be copied to dst.
			src.Set(ordered.Encode("x"), []byte("1"))
			src.Set(ordered.Encode("w"), []byte("2"))
			sv := mapToVecDB(src, dm)
			dst := storage.MemDB()
			// These should not be deleted from dst.
			dst.Set(ordered.Encode("y"), []byte("3"))
			dst.Set(ordered.Encode("z"), []byte("4"))
			dv := mapToVecDB(dst, sm)

			srcNonVec, srcVec := split(t, src)
			dstNonVec, dstVec := split(t, dst)
			syncVecDB(dv, sv)
			srcSyncNonVec, srcSyncVec := split(t, src)
			dstSyncNonVec, dstSyncVec := split(t, dst)

			if !maps.Equal(dstSyncVec, srcSyncVec) {
				t.Errorf("syncVecDB(dst=%v, src=%v): dst = %v; should equal src", dstVec, srcVec, dstSyncVec)
			}
			if !maps.Equal(dstNonVec, dstSyncNonVec) {
				t.Errorf("non-vector dst=%v should equal synced non-vector dst=%v", dstVec, dstSyncVec)
			}
			if !maps.Equal(srcNonVec, srcSyncNonVec) {
				t.Errorf("non-vector src=%v should equal synced non-vector dst=%v", srcVec, srcSyncVec)
			}
		}
	}
}

// key decodes k into a list of strings and returns their comma concatenation.
func key(k []byte) (string, error) {
	elems, err := ordered.DecodeAny(k)
	if err != nil {
		return "", err
	}
	var selems []string
	for _, e := range elems {
		selems = append(selems, e.(string))
	}
	return strings.Join(selems, ","), nil
}

// split breaks db into a non-vector and a vector segment and
// returns the two segments. The segmentation is based on whether
// a db key starts with "llm.Vector".
func split(t *testing.T, db storage.DB) (map[string]int, map[string]int) {
	nonVec, vec := make(map[string]int), make(map[string]int)
	for k, vf := range db.Scan(nil, ordered.Encode(ordered.Inf)) {
		sk, err := key(k)
		if err != nil {
			t.Fatal(err)
		}

		if strings.HasPrefix(sk, "llm.Vector") {
			var v llm.Vector
			v.Decode(vf())
			if len(v) == 0 {
				vec[sk] = 0 // for test vectors []byte("0")
			} else {
				vec[sk] = int(v[0])
			}
		} else {
			iv, err := strconv.Atoi(string(vf()))
			if err != nil {
				t.Fatal(err)
			}
			nonVec[sk] = iv
		}
	}
	return nonVec, vec

}

func mapToDB(m map[string]int) storage.DB {
	db := storage.MemDB()
	for k, v := range m {
		db.Set(ordered.Encode(k), []byte(strconv.Itoa(v)))
	}
	return db
}

func mapToVecDB(db storage.DB, m map[string]int) storage.VectorDB {
	vdb := storage.MemVectorDB(db, slog.Default(), "")
	for k, v := range m {
		vdb.Set(k, []float32{float32(v)})
	}
	return vdb
}

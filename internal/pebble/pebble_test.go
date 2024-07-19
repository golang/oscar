// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pebble

import (
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"testing"

	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

type testWriter struct{ t *testing.T }

func (w testWriter) Write(b []byte) (int, error) {
	w.t.Logf("%s", b)
	return len(b), nil
}

func TestDB(t *testing.T) {
	lg := testutil.Slogger(t)
	dir := t.TempDir()
	dbname := filepath.Join(dir, "db1")

	db, err := Open(lg, dbname)
	if err == nil {
		t.Fatal("Open nonexistent succeeded")
	}

	db, err = Create(lg, dbname)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = Create(lg, dbname)
	if err == nil {
		t.Fatal("Create already-existing succeeded")
	}

	db, err = Open(lg, dbname)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	storage.TestDB(t, db)
	storage.TestDBLock(t, db)

	if testing.Short() {
		return
	}

	// Test that MaybeApply handles very large batch.
	b := db.Batch()
	val := make([]byte, 1e6)
	pcg := rand.NewPCG(1, 2)
	applied := 0
	for key := range 500 {
		for i := 0; i < len(val); i += 8 {
			binary.BigEndian.PutUint64(val[i:], pcg.Uint64())
		}
		binary.BigEndian.PutUint64(val, uint64(key))
		b.Set([]byte(fmt.Sprint(key)), val)
		if b.MaybeApply() {
			if applied++; applied == 2 {
				break
			}
		}
	}
	b.Apply()

	for key := range 200 {
		val, ok := db.Get([]byte(fmt.Sprint(key)))
		if !ok {
			t.Fatalf("after batch, missing key %d", key)
		}
		if x := binary.BigEndian.Uint64(val); x != uint64(key) {
			t.Fatalf("Get(%d) = value for %d, want %d", key, x, key)
		}
	}
}

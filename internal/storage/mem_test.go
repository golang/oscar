// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package storage

import (
	"reflect"
	"testing"

	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/testutil"
)

func TestMemDB(t *testing.T) {
	db := MemDB()
	TestDB(t, db)
	TestDBLock(t, db)
}

func TestMemVectorDB(t *testing.T) {
	db := MemDB()
	TestVectorDB(t, func() VectorDB { return MemVectorDB(db, testutil.Slogger(t), "") })
}

type maybeDB struct {
	DB
	maybe bool
}

type maybeBatch struct {
	db *maybeDB
	Batch
}

func (db *maybeDB) Batch() Batch {
	return &maybeBatch{db: db, Batch: db.DB.Batch()}
}

func (b *maybeBatch) MaybeApply() bool {
	return b.db.maybe
}

// Test that when db.Batch.MaybeApply returns true,
// the memvector Batch MaybeApply applies the memvector ops.
func TestMemVectorBatchMaybeApply(t *testing.T) {
	db := &maybeDB{DB: MemDB()}
	vdb := MemVectorDB(db, testutil.Slogger(t), "")
	b := vdb.Batch()
	b.Set("apple3", embed("apple3"))
	if _, ok := vdb.Get("apple3"); ok {
		t.Errorf("Get(apple3) succeeded before batch apply")
	}
	b.MaybeApply() // should not apply because the db Batch does not apply
	if _, ok := vdb.Get("apple3"); ok {
		t.Errorf("Get(apple3) succeeded after MaybeApply that didn't apply")
	}
	db.maybe = true
	b.MaybeApply() // now should apply
	if _, ok := vdb.Get("apple3"); !ok {
		t.Errorf("Get(apple3) failed after MaybeApply that did apply")
	}
}

func TestMemVectorDBAll(t *testing.T) {
	db := &maybeDB{DB: MemDB()}
	vdb := MemVectorDB(db, testutil.Slogger(t), "")

	vdb.Set("apple1", embed("apple1"))
	vdb.Set("apple2", embed("apple2"))
	vdb.Delete("apple2")
	_, _ = vdb.Get("apple1")
	vdb.Set("apple3", embed("apple3"))
	vdb.Delete("apple3")
	vdb.Set("apple3", embed("apple3"))

	got := make(map[string]llm.Vector)
	for k, vec := range vdb.All() {
		got[k] = vec()
	}

	want := map[string]llm.Vector{
		"apple1": embed("apple1"),
		"apple3": embed("apple3"),
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("got %v;\nwant %v", got, want)
	}
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package storage

import (
	"testing"

	"golang.org/x/oscar/internal/testutil"
)

func TestMemDB(t *testing.T) {
	TestDB(t, MemDB())
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

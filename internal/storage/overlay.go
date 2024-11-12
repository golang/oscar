// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package storage

import (
	"bytes"
	"iter"
	"sync"
)

type overlayDB struct {
	MemLocker
	mu            sync.RWMutex
	base          DB
	overlay       *memDB
	deletedKeys   map[string]bool
	deletedRanges []keyRange
}

type keyRange struct {
	start, end []byte
}

// NewOverlayDB returns a DB that overlays a MemDB over a base DB.
// Reads happen from the overlay first, then the base.
// All writes go to the overlay.
func NewOverlayDB(base DB) DB {
	return &overlayDB{
		base:        base,
		overlay:     MemDB().(*memDB),
		deletedKeys: map[string]bool{},
	}
}

// Get returns the value associated with the key.
func (db *overlayDB) Get(key []byte) (val []byte, ok bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if oval, ok := db.overlay.Get(key); ok {
		return oval, true
	}

	if db.deleted(key) {
		return nil, false
	}
	return db.base.Get(key)
}

// Set sets the value associated with key to val.
func (db *overlayDB) Set(key, val []byte) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.setLocked(key, val)
}

func (db *overlayDB) setLocked(key, val []byte) {
	db.overlay.Set(key, val)
	delete(db.deletedKeys, string(key)) // save some space; not strictly necessary
}

// Delete deletes any entry with the given key.
func (db *overlayDB) Delete(key []byte) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.deleteLocked(key)
}

func (db *overlayDB) deleteLocked(key []byte) {
	db.overlay.Delete(key)
	db.deletedKeys[string(key)] = true
}

// DeleteRange deletes all entries with start ≤ key ≤ end.
func (db *overlayDB) DeleteRange(start, end []byte) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.deleteRangeLocked(start, end)
}

func (db *overlayDB) deleteRangeLocked(start, end []byte) {
	// TODO(maybe): consolidate ranges
	db.overlay.DeleteRange(start, end)
	db.deletedRanges = append(db.deletedRanges, keyRange{start, end})
}

// Scan returns an iterator over all key-value pairs
// in the range start ≤ key ≤ end.
// It does not guaranteed a consistent view of the DB (snapshot); keys and values
// may change during the iteration.
func (db *overlayDB) Scan(start, end []byte) iter.Seq2[[]byte, func() []byte] {
	return func(yield func([]byte, func() []byte) bool) {
		db.mu.RLock()
		locked := true
		defer func() {
			if locked {
				db.mu.RUnlock()
			}
		}()

		// Filter out all keys in base that have been deleted.
		fbase := filter2(db.base.Scan(start, end),
			func(k []byte, v func() []byte) bool { return !db.deleted(k) })
		// Merge all the keys in overlay with the undeleted ones in base.
		for k, vf := range unionFunc2(db.overlay.Scan(start, end), fbase, bytes.Compare) {
			// Release the lock so yield can call methods on db.
			db.mu.RUnlock()
			locked = false
			if !yield(k, vf) {
				return
			}
			db.mu.RLock()
			locked = true
		}
	}
}

// filter2 returns a sequence that consists of all the elements of s for which f returns true.
func filter2[K, V any](s iter.Seq2[K, V], f func(K, V) bool) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		for k, v := range s {
			if !f(k, v) {
				continue
			}
			if !yield(k, v) {
				return
			}
		}
	}
}

// unionFunc2 returns an iterator over all elements of s1 and s2, with keys in the same order.
// The keys of s1 and s2 must both be sorted according to cmp.
// If s1 and s2 have the same key, it is yielded once, with s1's value.
func unionFunc2[K, V any](s1, s2 iter.Seq2[K, V], cmp func(K, K) int) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		next, stop := iter.Pull2(s2)
		defer stop()

		k2, v2, ok := next()
		for k1, v1 := range s1 {
			for ok && cmp(k2, k1) < 0 {
				if !yield(k2, v2) {
					return
				}
				k2, v2, ok = next()
			}
			if !yield(k1, v1) {
				return
			}
			if cmp(k1, k2) == 0 {
				k2, v2, ok = next()
			}
		}
		for ; ok; k2, v2, ok = next() {
			if !yield(k2, v2) {
				return
			}
		}
	}
}

// deleted reports whether key is a deleted key.
// The result is only valid for db if key is not present in db.overlay;
// keys in db.overlay are not deleted, regardless of what this function reports.
// deleted must be called with the lock held.
func (db *overlayDB) deleted(key []byte) bool {
	if db.deletedKeys[string(key)] {
		return true
	}
	for _, r := range db.deletedRanges {
		if bytes.Compare(key, r.start) >= 0 && bytes.Compare(key, r.end) <= 0 {
			return true
		}
	}
	return false
}

// Batch returns a new batch.
func (db *overlayDB) Batch() Batch {
	return &overlayBatch{db: db}
}

// Flush flushes everything to persistent storage.
func (db *overlayDB) Flush() {
	// overlay is a memDB and base is never written; nothing to flush.
}

func (db *overlayDB) Close() {
	db.base.Close()
	db.overlay.Close()
}

func (db *overlayDB) Panic(msg string, args ...any) {
	Panic(msg, args...)
}

// An overlayBatch is a Batch for an overlayDB.
type overlayBatch struct {
	db  *overlayDB // underlying database
	ops []func()   // operations to apply
}

func (b *overlayBatch) Set(key, val []byte) {
	if len(key) == 0 {
		b.db.Panic("overlaydb batch set: empty key")
	}
	k := bytes.Clone(key)
	v := bytes.Clone(val)
	b.ops = append(b.ops, func() { b.db.setLocked(k, v) })
}

func (b *overlayBatch) Delete(key []byte) {
	k := bytes.Clone(key)
	b.ops = append(b.ops, func() { b.db.deleteLocked(k) })
}

func (b *overlayBatch) DeleteRange(start, end []byte) {
	s := bytes.Clone(start)
	e := bytes.Clone(end)
	b.ops = append(b.ops, func() { b.db.deleteRangeLocked(s, e) })
}

func (b *overlayBatch) MaybeApply() bool {
	return false
}

func (b *overlayBatch) Apply() {
	b.db.mu.Lock()
	defer b.db.mu.Unlock()

	for _, op := range b.ops {
		op()
	}
	b.ops = nil
}

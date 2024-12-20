// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package timed implements modification-time-indexed storage
// that can be accessed in modification-time order as well as key order.
//
// Timed storage builds on top of a basic key-value storage by
// maintaining two actual database entries for each logical entry.
// The two actual entries for the logical entry (kind, key) → val are:
//
//   - (kind, key) → (modtime, val)
//   - (kindByTime, modtime, key) → ()
//
// The “kind” is a key namespace that allows maintaining multiple
// independent sets of modification-time-indexed storage in a
// single [storage.DB].
//
// The “modtime” (of type [DBTime]) is an opaque timestamp representing the time when
// the logical entry was last set. Other than being a monotonically
// increasing integer, no specific semantics are guaranteed about the
// meaning of the dbtime.
//
// An [Entry] represents a single logical entry:
//
//	type Entry struct {
//		ModTime DBTime // time entry was written
//		Kind   string // key namespace
//		Key    []byte // key
//		Val    []byte // value
//	}
//
// The basic [Get], [Set], [Scan], [Delete], and [DeleteRange] functions
// are analogous to those in [storage.DB]
// but modified to accommodate the new entries:
//
//   - Get(db, kind, key) returns an *Entry.
//     The db is the underlying storage being used.
//
//   - Set(db, batch, kind, key, val) adds or replaces an entry.
//     The db is the underlying storage, which is consulted,
//     but the actual modifications are written to batch,
//     so that the two different actual entries can be applied
//     as a single unit.
//
//   - Scan(db, kind, start, end) returns an iterator yielding *Entry.
//
//   - Delete(db, batch, kind, key) deletes an entry.
//
//   - DeleteRange(db, batch, kind, start, end) deletes a range of entries.
//
// In addition to these operations, the time index enables one new operation:
//
//   - After(db, kind, dbtime) returns an iterator yielding entries
//     updated after dbtime, ordered by time of update.
//
// In typical usage, a client stores the latest e.DBTime it has observed
// and then uses that value in a future call to After to find only
// recent entries. The [Watcher] encapsulates that pattern and
// adds mutual exclusion so that multiple processes or goroutines
// using the same Watcher will not run concurrently.
package timed

import (
	"iter"
	"log/slog"
	"sync/atomic"
	"time"

	"golang.org/x/oscar/internal/storage"
	"rsc.io/ordered"
)

// A DBTime is an opaque timestamp representing the time when
// a database entry was last set. Timestamps increase monotonically:
// comparing two times from entries of the same kind indicates
// which entry was written first.
// Otherwise, timestamps are opaque and have no specific meaning.
type DBTime int64

var lastTime atomic.Int64

// now returns the current DBTime.
// The implementation assumes accurate time-keeping on the systems where it runs,
// so that if the program is restarted, the new instance will not see times before
// the ones the old instance did.
//
// Packages storing information in the database can create separate
// indexes by DBTime to enable incremental processing of new data.
func now() DBTime {
	for {
		old := lastTime.Load()
		t := time.Now().UnixNano()
		if t <= old {
			t = old + 1
		}
		if lastTime.CompareAndSwap(old, t) {
			return DBTime(t)
		}
	}
}

// An Entry is a single entry written to the time-indexed storage.
type Entry struct {
	ModTime DBTime // time entry was written
	Kind    string // key namespace
	Key     []byte // key
	Val     []byte // value
}

// Set adds to b the database updates to set (kind, key) → val,
// including updating the time index.
// It returns the DBTime associated with the entry.
func Set(db storage.DB, b storage.Batch, kind string, key, val []byte) DBTime {
	t := now()
	dkey := append(ordered.Encode(kind), key...)
	if old, ok := db.Get(dkey); ok {
		var oldT int64
		if _, err := ordered.DecodePrefix(old, &oldT); err != nil {
			// unreachable unless corrupt storage
			db.Panic("timed.Set decode old", "dkey", storage.Fmt(dkey), "old", storage.Fmt(old), "err", err)
		}
		b.Delete(append(ordered.Encode(kind+"ByTime", oldT), key...))
	}
	b.Set(append(ordered.Encode(kind+"ByTime", int64(t)), key...), nil)
	b.Set(dkey, append(ordered.Encode(int64(t)), val...))
	return t
}

// Delete adds to b the database updates to delete the value corresponding to (kind, key), if any.
func Delete(db storage.DB, b storage.Batch, kind string, key []byte) {
	dkey := append(ordered.Encode(kind), key...)
	dval, ok := db.Get(dkey)
	if !ok {
		return
	}
	var t int64
	if _, err := ordered.DecodePrefix(dval, &t); err != nil {
		// unreachable unless corrupt storage
		db.Panic("timed.Delete decode dval", "dkey", storage.Fmt(dkey), "dval", storage.Fmt(dval), "err", err)
	}
	b.Delete(dkey)
	b.Delete(append(ordered.Encode(kind+"ByTime", t), key...))
}

// Get retrieves the value corresponding to (kind, key).
func Get(db storage.DB, kind string, key []byte) (*Entry, bool) {
	dkey := append(ordered.Encode(kind), key...)
	dval, ok := db.Get(dkey)
	if !ok {
		return nil, false
	}
	var t int64
	val, err := ordered.DecodePrefix(dval, &t)
	if err != nil {
		// unreachable unless corrupt storage
		db.Panic("GetTimed decode", "dkey", storage.Fmt(dkey), "dval", storage.Fmt(dval), "err", err)
	}
	return &Entry{DBTime(t), kind, key, val}, true
}

// Scan returns an iterator over entries (kind, key) → val with start ≤ key ≤ end.
func Scan(db storage.DB, kind string, start, end []byte) iter.Seq[*Entry] {
	dstart := append(ordered.Encode(kind), start...)
	dend := append(ordered.Encode(kind), end...)
	return func(yield func(*Entry) bool) {
		for dkey, dvalf := range db.Scan(dstart, dend) {
			dval := dvalf()
			key, err := ordered.DecodePrefix(dkey, nil) // drop kind
			if err != nil {
				// unreachable unless corrupt storage
				db.Panic("ScanTimed decode", "dkey", storage.Fmt(dkey), "err", err)
			}
			var t int64
			val, err := ordered.DecodePrefix(dval, &t)
			if err != nil {
				// unreachable unless corrupt storage
				db.Panic("ScanTimed decode", "dkey", storage.Fmt(dkey), "dval", storage.Fmt(dval), "err", err)
			}
			if !yield(&Entry{DBTime(t), kind, key, val}) {
				return
			}
		}
	}
}

// DeleteRange adds to b the database updates to delete all entries
// (kind, key) → val with start ≤ key ≤ end.
// To allow deleting an arbitrarily large range, DeleteRange calls
// b.MaybeApply after adding the updates for each (kind, key).
// The effect is that a large range deletion may not be applied atomically to db.
// The caller still needs to use b.Apply to apply the final updates
// (or, for small ranges, all of them).
func DeleteRange(db storage.DB, b storage.Batch, kind string, start, end []byte) {
	for e := range Scan(db, kind, start, end) {
		b.Delete(append(ordered.Encode(kind), e.Key...))
		b.Delete(append(ordered.Encode(kind+"ByTime", int64(e.ModTime)), e.Key...))
		b.MaybeApply()
	}
}

// ScanAfter returns an iterator over entries in the database
// of the given kind that were set after DBTime t.
// If filter is non-nil, ScanAfter omits entries for which filter(e.Key) returns false
// and avoids the overhead of loading e.Val for those entries.
func ScanAfter(lg *slog.Logger, db storage.DB, kind string, t DBTime, filter func(key []byte) bool) iter.Seq[*Entry] {
	return func(yield func(*Entry) bool) {
		start, end := ordered.Encode(kind+"ByTime", int64(t+1)), ordered.Encode(kind+"ByTime", ordered.Inf)
		for tkey := range db.Scan(start, end) {
			var t int64
			key, err := ordered.DecodePrefix(tkey, nil, &t) // drop kind
			if err != nil {
				// unreachable unless corrupt storage
				db.Panic("storage.After decode", "tkey", storage.Fmt(tkey), "err", err)
			}
			if filter != nil && !filter(key) {
				continue
			}
			dkey := append(ordered.Encode(kind), key...)
			dval, ok := db.Get(dkey)
			if !ok {
				// Stale entries might happen if Set is called multiple times
				// for the same key in a single batch, along with a later Delete,
				// or Set+Delete in a single batch. Ignore.
				lg.Info("timed stale missing", "tkey", storage.Fmt(tkey), "dkey", storage.Fmt(dkey))
				continue
			}
			var t2 int64
			val, err := ordered.DecodePrefix(dval, &t2)
			if err != nil {
				// unreachable unless corrupt storage
				db.Panic("storage.After val decode", "key", storage.Fmt(key), "dkey", storage.Fmt(dkey), "val", storage.Fmt(val), "err", err)
			}
			if t < t2 {
				// Stale entries might happen if Set is called multiple times
				// for the same key in a single batch.
				// The second Set will not see the first one's time entry to delete it.
				// These should be rare.
				// Skip this one and wait until we see the index entry for t2.
				lg.Info("timed stale out of order", "tkey", storage.Fmt(tkey), "dkey", storage.Fmt(dkey), "dval-time", t2)
				continue
			}
			if t > t2 {
				// unreachable unless corruption:
				// new index entries with old data should not happen.
				db.Panic("timed.ScanAfter mismatch", "tkey", storage.Fmt(tkey), "dkey", storage.Fmt(dkey), "dval", storage.Fmt(dval))
			}
			if !yield(&Entry{DBTime(t), kind, key, val}) {
				return
			}
		}
	}
}

// A Watcher is a named cursor over recently modified time-stamped key-value pairs
// (written using [Set]).
// The state of the cursor is stored in the underlying database so that
// it persists across program restarts and is shared by all clients of the
// database.
//
// Across all Watchers with the same db, kind, and name, database locks
// are used to ensure that only one at a time can iterate over [Watcher.Recent].
// This provides coordination when multiple instances of a program that
// notice there is work to be done and more importantly avoids those
// instances conflicting with each other.
//
// The Watcher's state (most recent dbtime marked old)
// is stored in the underlying database using the key
// ordered.Encode(kind+"Watcher", name),
// and while a Watcher is iterating, it locks a database lock
// with the same name as that key.
type Watcher[T any] struct {
	slog   *slog.Logger
	db     storage.DB
	dkey   []byte
	kind   string
	decode func(*Entry) T
	locked atomic.Bool
	latest atomic.Int64 // highest known DBTime marked old, for fast retrieval by metrics
}

// NewWatcher returns a new named Watcher reading keys of the given kind from db.
// Use [Watcher.Recent] to iterate over recent entries.
//
// The name distinguishes this Watcher from other Watchers watching the same kind of keys
// for different purposes.
//
// The Watcher applies decode(e) to each time-stamped Entry to obtain the T returned
// in the iteration.
func NewWatcher[T any](lg *slog.Logger, db storage.DB, name, kind string, decode func(*Entry) T) *Watcher[T] {
	w := &Watcher[T]{
		slog:   lg,
		db:     db,
		dkey:   ordered.Encode(kind+"Watcher", name),
		kind:   kind,
		decode: decode,
	}
	// Set w.latest to current DB value.
	w.cutoffUnlocked()
	return w
}

func (w *Watcher[T]) lock() {
	if w.locked.Load() {
		w.db.Panic("timed.Watcher already locked")
	}
	w.db.Lock(string(w.dkey))
	w.locked.Store(true)
}

func (w *Watcher[T]) unlock() {
	if !w.locked.Load() {
		w.db.Panic("timed.Watcher not locked")
	}
	w.db.Unlock(string(w.dkey))
	w.locked.Store(false)
}

func (w *Watcher[T]) cutoff() DBTime {
	if !w.locked.Load() {
		// unreachable unless called wrong in this file
		w.db.Panic("timed.Watcher not locked")
	}
	return w.cutoffUnlocked()
}

// cutoffUnlocked returns the value of the watcher key in the DB.
// (The key is the maximum of calls to [Watcher.MarkOld].)
// It also updates [Watcher.latest].
func (w *Watcher[T]) cutoffUnlocked() DBTime {
	var t int64
	if dval, ok := w.db.Get(w.dkey); ok {
		if err := ordered.Decode(dval, &t); err != nil {
			// unreachable unless corrupt storage
			w.db.Panic("watcher decode", "dval", storage.Fmt(dval), "err", err)
		}
	}
	if w.latest.Load() < t {
		w.latest.Store(t)
	}
	return DBTime(t)
}

// Recent returns an iterator over recent entries,
// meaning those entries set after the last time recorded using [Watcher.MarkOld].
// The events are ordered by the time they were last [Set] (that is, by ModTime).
//
// When the iterator is invoked or ranged over, it acquires a database lock
// corresponding to (db, name, kind), to prevent other racing instances of
// the same Watcher from visiting the same entries.
// It releases the lock automatically when the iteration ends or is stopped.
//
// The iterator must be used from only a single goroutine at a time,
// or else it will panic reporting “Watcher already locked”.
// This means that while an iterator can be used multiple times in
// sequence, it cannot be used from multiple goroutines,
// nor can a new iteration be started inside an existing one.
// (If a different process holds the lock, the iterator will wait for that process.
// The in-process lock check aims to diagnose simple deadlocks.)
func (w *Watcher[T]) Recent() iter.Seq[T] {
	return w.RecentFiltered(nil)
}

// RecentFiltered is like [Watcher.Recent], except it applies the given
// filter to the keys. The filter uses the same semantics as in [ScanAfter].
func (w *Watcher[T]) RecentFiltered(filter func(key []byte) bool) iter.Seq[T] {
	return func(yield func(T) bool) {
		w.lock()
		defer func() {
			w.Flush()
			w.unlock()
		}()

		for t := range ScanAfter(w.slog, w.db, w.kind, w.cutoff(), filter) {
			if !yield(w.decode(t)) {
				return
			}
		}
	}
}

// Restart resets the event watcher so that the next iteration over new events
// will start at the earliest possible event.
// In effect, Restart undoes all previous calls to MarkOld.
// Restart must be not be called during an iteration.
func (w *Watcher[T]) Restart() {
	w.lock()
	defer w.unlock()

	w.db.Delete(w.dkey)
}

// MarkOld marks entries at or before t as “old”,
// meaning they will no longer be iterated over by [Watcher.Recent].
// A future call to Recent, perhaps even on a different Watcher
// with the same configuration,
// will start its iteration starting immediately after time t.
//
// If a newer time t has already been marked “old” in this watcher,
// then MarkOld(t) is a no-op.
//
// MarkOld must only be called during an iteration over Recent,
// so that the database lock corresponding to this Watcher is held.
// In the case of a process crash before the iteration completes,
// the effect of MarkOld may be lost.
// Calling [Watcher.Flush] forces the state change to the underlying database.
func (w *Watcher[T]) MarkOld(t DBTime) {
	if !w.locked.Load() {
		w.db.Panic("timed.Watcher.MarkOld unlocked")
	}
	if t <= w.cutoff() {
		return
	}
	w.db.Set(w.dkey, ordered.Encode(int64(t)))
	w.latest.Store(int64(t))
}

// Flush flushes the definition of recent (changed by MarkOld) to the database.
// Flush is called automatically at the end of an iteration,
// but it can be called explicitly during a long iteration as well.
func (w *Watcher[T]) Flush() {
	w.db.Flush()
}

// Latest returns the latest known DBTime marked old by the Watcher.
// It does not require the lock to be held.
func (w *Watcher[T]) Latest() DBTime {
	return DBTime(w.latest.Load())
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package firestore

import (
	"context"
	"encoding/hex"
	"fmt"
	"iter"
	"log/slog"
	"math/rand/v2"
	"slices"
	"time"

	"cloud.google.com/go/firestore"
	"golang.org/x/oscar/internal/storage"
	"google.golang.org/api/option"
)

// DB is a connection to a Firestore database.
// It implements [storage.DB].
type DB struct {
	*fstore
	uid    int64 // unique ID, to identify lock owners
	locks  *firestore.CollectionRef
	values *firestore.CollectionRef
}

// NewDB constructs a [DB] with the given GCP logger, project ID, Firestore database, and client options.
// The projectID must not be empty.
// If the database is empty, the default database will be used.
//
// Key-value pairs are stored in the collection "values".
// Locks are stored in the collection "locks".
func NewDB(ctx context.Context, lg *slog.Logger, projectID, database string, opts ...option.ClientOption) (*DB, error) {
	fs, err := newFstore(ctx, lg, projectID, database, opts)
	if err != nil {
		return nil, err
	}
	return &DB{
		fstore: fs,
		uid:    rand.Int64(),
		locks:  fs.client.Collection("locks"),
		values: fs.client.Collection("values"),
	}, nil
}

// vars for testing
var (
	// how long to wait before stealing a lock
	lockTimeout = 2 * time.Minute

	timeSince = time.Since
)

// Lock implements [storage.DB.Lock].
// Locks are stored as documents in the "locks" collection of the database.
// If the document exists its content is the UID of the [DB] that holds it,
// as set in [NewDB].
// It is an error for a DB to unlock a lock that it doesn't own.
// To avoid deadlock due to failed processes, locks time out after a reasonable
// period of time.
// There is no way to change the timeout value, and no way to renew a lease on a lock.
func (db *DB) Lock(name string) {
	// Wait for the lock in a separate function to avoid defers inside a loop, consuming
	// memory on each iteration.
	for !db.waitForLock(name) {
	}
}

// waitForLock waits for the lock to become available.
// It returns true if it acquires the lock.
// It returns false if the snapshot iterator timed out without the lock
// being acquired.
func (db *DB) waitForLock(name string) bool {
	// Use a snapshot iterator to iterate over changing states of the document.
	// It yields its first value immediately, and subsequent values only when
	// the document changes state.
	// We want the iterator to time out eventually, or an orphaned lock document
	// that remains unchanged could cause it to wait indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	defer cancel()
	dr := db.locks.Doc(encodeLockName(name))
	iter := dr.Snapshots(ctx)
	defer iter.Stop()
	for {
		_, err := iter.Next()
		if err == nil {
			if db.tryLock(name) {
				return true
			}
			// Wait for a change in the lock document.
			continue
		}
		if isTimeout(err) {
			// The lock document may not have changed for lockTimeout;
			// assume that's true and try to steal it.
			return db.tryLock(name)
		}
		// unreachable except for bad DB
		db.Panic("firestore waiting for lock", "name", name, "err", err)
	}
}

// tryLock tries to acquire the named lock in a transaction.
func (db *DB) tryLock(name string) (res bool) {
	db.runTransaction(func(ctx context.Context, tx *firestore.Transaction) {
		uid, createTime := db.getLock(tx, name)
		if createTime.IsZero() || timeSince(createTime) > lockTimeout {
			// Lock does not exist or timed out.
			if !createTime.IsZero() {
				db.slog.Warn("taking lock", "name", name, "old", uid, "new", db.uid)
			}
			db.setLock(tx, name)
			res = true
		} else {
			res = false
		}
	})
	return res
}

// Unlock releases the lock. It panics if the lock isn't locked by this DB.
func (db *DB) Unlock(name string) {
	db.runTransaction(func(ctx context.Context, tx *firestore.Transaction) {
		uid, createTime := db.getLock(tx, name)
		if createTime.IsZero() {
			db.Panic("unlock of never locked key", "key", name)
		}
		if uid != db.uid {
			db.Panic("unlocker is not owner", "key", name)
		}
		db.deleteLock(tx, name)
	})
}

// A lock describes a lock in firestore.
// The value consists of the UID of the DB that acquired the lock.
type lock struct {
	UID int64
}

// setLock sets the value of the named lock in the DB, along with its creation time.
func (db *DB) setLock(tx *firestore.Transaction, name string) {
	db.set(tx, db.locks, encodeLockName(name), lock{db.uid})
}

// getLock returns the value of the named lock in the DB.
func (db *DB) getLock(tx *firestore.Transaction, name string) (int64, time.Time) {
	ds := db.get(tx, db.locks, encodeLockName(name))
	if ds == nil {
		return 0, time.Time{}
	}
	uid := dataTo[lock](db.fstore, ds).UID
	return uid, ds.CreateTime
}

// deleteLock deletes the named lock in the DB.
func (db *DB) deleteLock(tx *firestore.Transaction, name string) {
	db.delete(tx, db.locks, encodeLockName(name))
}

func encodeLockName(name string) string {
	return hex.EncodeToString([]byte(name))
}

// Set implements [storage.DB.Set].
func (db *DB) Set(key, val []byte) {
	if len(key) == 0 {
		db.Panic("firestore set: empty key")
	}
	db.set(nil, db.values, encodeKey(key), value{val})
}

// Get implements [storage.DB.Get].
func (db *DB) Get(key []byte) ([]byte, bool) {
	ekey := encodeKey(key)
	ds := db.get(nil, db.values, ekey)
	if ds == nil {
		return nil, false
	}
	return dataTo[value](db.fstore, ds).V, true
}

// Delete implements [storage.DB.Delete].
func (db *DB) Delete(key []byte) {
	db.delete(nil, db.values, encodeKey(key))
}

// DeleteRange implements [storage.DB.DeleteRange].
func (db *DB) DeleteRange(start, end []byte) {
	db.deleteRange(db.values, encodeKey(start), encodeKey(end))
}

// Scan implements [storage.DB.Scan].
func (db *DB) Scan(start, end []byte) iter.Seq2[[]byte, func() []byte] {
	return func(yield func(key []byte, valf func() []byte) bool) {
		for ds := range db.scan(nil, db.values, encodeKey(start), encodeKey(end)) {
			if !yield(decodeKey(ds.Ref.ID), func() []byte { return dataTo[value](db.fstore, ds).V }) {
				return
			}
		}
	}
}

// Batch implements [storage.DB.Batch].
func (db *DB) Batch() storage.Batch {
	return &dbBatch{db.newBatch(db.values)}
}

type dbBatch struct {
	b *batch
}

// Delete implements [storage.Batch.Delete].
func (b *dbBatch) Delete(key []byte) {
	b.b.delete(encodeKey(key))
}

// DeleteRange implements [storage.Batch.DeleteRange].
func (b *dbBatch) DeleteRange(start, end []byte) {
	b.b.deleteRange(encodeKey(start), encodeKey(end))
}

// Set implements [storage.Batch.Set].
func (b *dbBatch) Set(key, val []byte) {
	if len(key) == 0 {
		b.b.f.Panic("firestore batch set: empty key")
	}
	// TODO(jba): account for size of encoded struct.
	b.b.set(encodeKey(key), value{slices.Clone(val)}, len(val))
}

// MaybeApply implements [storage.Batch.MaybeApply].
func (b *dbBatch) MaybeApply() bool {
	return b.b.maybeApply()
}

// Apply implements [storage.Batch.Apply].
func (b *dbBatch) Apply() {
	b.b.apply()
}

// encodeKey converts k to a string, preserving ordering.
func encodeKey(k []byte) string {
	return hex.EncodeToString(k)
}

func decodeKey(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		// unreachable except for bad DB
		panic(fmt.Sprintf("decodeKey(%q) failed: %v", s, err))
	}
	return b
}

// A value is a [DB] value as a Firestore document.
// (Firestore values must be maps or structs.)
type value struct {
	V []byte
}

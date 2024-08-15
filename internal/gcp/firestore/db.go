// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package firestore

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"maps"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"golang.org/x/oscar/internal/gcp/grpcerrors"
	"golang.org/x/oscar/internal/storage"
	"google.golang.org/api/option"
)

// DB is a connection to a Firestore database.
// It implements [storage.DB].
type DB struct {
	*fstore
	uid         int64 // unique ID, to identify lock owners
	locks       *firestore.CollectionRef
	values      *firestore.CollectionRef
	activeLocks *activeLocks // locks held by this DB
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
		fstore:      fs,
		uid:         rand.Int64(),
		locks:       fs.client.Collection("locks"),
		values:      fs.client.Collection("values"),
		activeLocks: &activeLocks{locks: make(map[string]*locked)},
	}, nil
}

// vars for testing
var (
	// how long to wait before stealing a lock
	lockTimeout = 2 * time.Minute
	// how often to renew a held lock
	lockRenew = 1 * time.Minute

	timeSince = time.Since
)

// Lock implements [storage.DB.Lock].
// Locks are stored as documents in the "locks" collection of the database.
// If the document exists its content is the UID of the [DB] that holds it,
// as set in [NewDB].
// It is an error for a DB to unlock a lock that it doesn't own.
// If the calling process fails, the lock times out after two minutes.
// Otherwise, locks are renewed every minute if Unlock is not called.
// There is no way to change the timeout or renew value.
func (db *DB) Lock(name string) {
	// Wait for the lock in a separate function to avoid defers inside a loop, consuming
	// memory on each iteration.
	for !db.waitForLock(name) {
	}
}

// waitForLock waits for the lock to become available.
// It returns true if it acquires the lock, or false if it cannot
// acquire the lock after lockTimeout elapses.
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
			if db.lock(name) {
				return true
			}
			// We didn't get the lock; wait for a change in the lock document.
			continue
		}
		if grpcerrors.IsTimeout(err) {
			// The lock document may not have changed for lockTimeout;
			// assume that's true and try to steal it.
			return db.lock(name)
		}
		// unreachable except for bad DB
		db.Panic("firestore waiting for lock", "name", name, "err", err)
	}
}

// lock tries to acquire the named lock in a transaction.
// It reports whether the lock was acquired.
// (The lock is not acquired if it is already held
// by this or another DB, and not expired.)
// On success, lock starts a goroutine to renew the acquired lock.
// lock panics if a lock with the given name is already held by this DB.
func (db *DB) lock(name string) bool {
	// Lock is held by this DB, no need to check firestore.
	if db.activeLocks.isHeld(name) {
		return false
	}

	held := db.lockTx(name)
	if !held {
		return false
	}

	lk := db.activeLocks.lock(name)
	if lk == nil {
		db.Panic("lock of held lock", "name", name, "uid", db.uid)
	}

	// We have the lock. Renew it every minute until unlocked.
	go db.manageLock(name, lk)
	return held
}

// lockTx tries to acquire the named lock in a transaction.
// It reports whether the lock was acquired.
// (The lock is not acquired if it is already held
// by another DB, and not expired.)
func (db *DB) lockTx(name string) (held bool) {
	db.runTransaction(func(ctx context.Context, tx *firestore.Transaction) {
		uid, createTime, updateTime := db.getLock(tx, name)
		if createTime.IsZero() {
			db.setLock(tx, name)
			held = true
			return
		}

		if elapsed := timeSince(updateTime); elapsed > lockTimeout {
			db.slog.Warn("taking expired lock", "name", name, "old", uid, "new", db.uid, "elapsed", elapsed)
			db.setLock(tx, name)
			held = true
			return
		}
	})
	return held
}

// manageLock renews the named lock in firestore every minute until
// Unlock is called, at which point it unlocks the lock in firestore.
// manageLock panics if the lock cannot be renewed or unlocked.
// The named lock must have already been acquired by this DB.
func (db *DB) manageLock(name string, lk *locked) {
	for {
		select {
		case <-lk.unlock:
			db.unlockTx(name)
			// Tell Unlock that it is safe to return.
			close(lk.unlocked)
			return
		case <-time.After(lockRenew):
			db.slog.Info("renewing lock", "name", name, "uid", db.uid)
			db.renewTx(name)
		}
	}
}

// unlockTx unlocks the named lock in a firestore transaction.
// It panics if the lock is already unlocked or held by another DB.
func (db *DB) unlockTx(name string) {
	db.runTransaction(func(ctx context.Context, tx *firestore.Transaction) {
		uid, createTime, _ := db.getLock(tx, name)
		if createTime.IsZero() {
			db.Panic("unlock of never locked key", "key", name)
		}
		if uid != db.uid {
			db.Panic("unlocker is not owner", "key", name)
		}
		db.deleteLock(tx, name)
	})
}

// renewTx renews the named lock in a firestore transaction,
// It panics if the locks is unlocked, expired or held by another DB.
func (db *DB) renewTx(name string) {
	db.runTransaction(func(ctx context.Context, tx *firestore.Transaction) {
		uid, createTime, updateTime := db.getLock(tx, name)
		if createTime.IsZero() {
			db.Panic("can't renew unlocked lock", "name", name, "uid", db.uid)
		}
		if uid != db.uid {
			db.Panic("can't renew lock owned by another DB", "name", name, "uid", db.uid, "owner", uid)
		}
		if elapsed := timeSince(updateTime); elapsed > lockTimeout {
			db.Panic("can't renew expired lock", "name", name, "uid", db.uid, "elapsed", elapsed)
		}
		db.setLock(tx, name)
	})
}

// activeLocks represents the locks currently held by a DB.
// It allows a DB to remember which locks it has locked and
// unlocked, and to stop attempting to renew a lock that is unlocked.
type activeLocks struct {
	mu sync.Mutex
	// lock names to active locks
	locks map[string]*locked
}

// Names returns the names of all the active locks.
func (a *activeLocks) Names() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Sorted(maps.Keys(a.locks))
}

// A locked represents a currently held lock.
// It is created by db.Lock and used to co-ordinate between db.Unlock
// and db.manageLock.
type locked struct {
	unlock   chan struct{} // closed by Unlock
	unlocked chan struct{} // closed by manageLock
}

func (a *activeLocks) isHeld(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	_, ok := a.locks[name]
	return ok
}

// lock returns a new active lock, which is also
// stored in the locks map under the given name.
// It returns nil if there is already an active lock with
// the given name.
func (a *activeLocks) lock(name string) *locked {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, ok := a.locks[name]; ok {
		return nil
	}

	l := &locked{
		unlock:   make(chan struct{}),
		unlocked: make(chan struct{}),
	}
	a.locks[name] = l
	return l
}

// unlock unlocks the active lock with the given name
// and waits for renew to finish before returning.
// It returns an error if the lock is not held by this DB.
func (a *activeLocks) unlock(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	lk, ok := a.locks[name]
	if !ok {
		return errors.New("unlock of unlocked or unowned lock")
	}

	close(lk.unlock)
	delete(a.locks, name)

	// Wait for manageLock to delete the lock in firestore.
	<-lk.unlocked
	return nil
}

// Unlock releases the lock. It panics if the lock isn't locked by this DB.
func (db *DB) Unlock(name string) {
	if err := db.activeLocks.unlock(name); err != nil {
		db.Panic("could not unlock", "name", name, "uid", db.uid, "err", err)
	}
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

// getLock returns the value of the named lock in the DB and the times
// it was created and last updated.
func (db *DB) getLock(tx *firestore.Transaction, name string) (uid int64, created time.Time, updated time.Time) {
	ds := db.get(tx, db.locks, encodeLockName(name))
	if ds == nil {
		return 0, time.Time{}, time.Time{}
	}
	uid = dataTo[lock](db.fstore, ds).UID
	return uid, ds.CreateTime, ds.UpdateTime
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

// decodeKey decodes an encoded key back to the original.
func decodeKey(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		// unreachable except for bad DB
		panic(fmt.Sprintf("decodeKey(%q) failed: %v", s, err))
	}
	return b
}

// keyAfter returns the lexically next encoded key after s.
func keyAfter(s string) string {
	return s + "00"
}

// A value is a [DB] value as a Firestore document.
// (Firestore values must be maps or structs.)
type value struct {
	V []byte
}

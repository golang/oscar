// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package firestore

import (
	"context"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oscar/internal/gcp/gcpconfig"
	"golang.org/x/oscar/internal/grpcrr"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

const firestoreTestDatabase = "test"

func TestDB(t *testing.T) {
	rr, project := openRR(t, "testdata/db.grpcrr")
	defer func() {
		if err := rr.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	ctx := context.Background()

	db, err := NewDB(ctx, testutil.Slogger(t), project, firestoreTestDatabase, rr.ClientOptions()...)
	if err != nil {
		t.Fatal(err)
	}
	// Use a fixed UID for deterministic record/replay.
	db.uid = 1
	defer db.Close()

	// Make the timeout short, in case the locks exist from a previous run.
	defer func(lt time.Duration) {
		lockTimeout = lt
	}(lockTimeout)
	lockTimeout = 2 * time.Second

	storage.TestDB(t, db)

	// Test Batch.DeleteRange with MaybeApply.
	b := db.Batch()
	b.DeleteRange([]byte("a"), []byte("b"))
	applied := b.MaybeApply()
	if !applied {
		t.Error("MaybeApply with DeleteRange did not apply")
	}
}

func TestLock(t *testing.T) {
	// The lock tests cannot be run with record/replay because they depend too much on time and state.
	// They are also slow, so skip them in short mode.
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	projectID, err := gcpconfig.Project()
	if err != nil {
		t.Skipf("skipping: %v", err)
	}

	defer func(lt, lr time.Duration) {
		lockTimeout = lt
		lockRenew = lr
	}(lockTimeout, lockRenew)
	lockRenew = 1 * time.Second
	lockTimeout = 2 * time.Second

	t.Run("lock-renew-unlock", func(t *testing.T) {
		db, name := newTestDB(t, projectID)

		// Lock will renew with no errors.
		db.Lock(name)
		time.Sleep(lockRenew * 2)
		db.Unlock(name)

		// Lock will be re-acquired with no errors.
		db.Lock(name)
		time.Sleep(lockRenew * 2)
		db.Unlock(name)
	})

	t.Run("basic", func(t *testing.T) {
		db, _ := newTestDB(t, projectID)
		storage.TestDBLock(t, db)
	})

	t.Run("timeout", func(t *testing.T) {
		db1, name := newTestDB(t, projectID)
		db2, _ := newTestDB(t, projectID)

		c1, c2 := make(chan struct{}), make(chan struct{})

		go func() {
			<-c1
			// db2 can steal the lock after it times out.
			db2.Lock(name)
			db2.Unlock(name)
			close(c2)
		}()

		// Simulate the case in which db1 manages to lock the lock
		// in firestore but never unlocks it.
		if held := db1.lockTx(name); !held {
			t.Fatal("could not write lock to firestore")
		}
		close(c1)

		select {
		case <-c2:
			// Success.
		case <-time.After(2 * lockTimeout):
			t.Fatal("lock timeout didn't happen in time")
		}
	})

	t.Run("owner", func(t *testing.T) {
		db, name := newTestDB(t, projectID)
		db2, _ := newTestDB(t, projectID)

		testutil.StopPanic(func() {
			db.Lock(name)
			db2.Unlock(name)
			t.Error("unlock wrong owner did not panic")
		})
	})
}

// unlockAll unlocks all the locks held by db.
// For testing.
func unlockAll(db *DB) {
	for _, name := range db.activeLocks.Names() {
		db.Unlock(name)
	}
}

// newTestDB returns a new DB and the name of a lock that
// can be used for tests.
// It ensures that the lock name does not already exist in the
// test firestore DB.
func newTestDB(t *testing.T, projectID string) (_ *DB, lock string) {
	db, err := NewDB(context.Background(), testutil.Slogger(t), projectID, firestoreTestDatabase)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		unlockAll(db)
		db.Close()
	})
	// Pick a random name (with the test name as a prefix) to avoid contention.
	lock = fmt.Sprintf("%s%03d", t.Name(), rand.N(1000))
	db.deleteLock(nil, lock) // Ensure the lock is not present.
	return db, lock
}

func TestErrors(t *testing.T) {
	testutil.StopPanic(func() {
		var b batch
		b.set("x", nil, 1)
		t.Error("batch.set does not panic on nil value")
	})
}

func openRR(t *testing.T, file string) (_ *grpcrr.RecordReplay, projectID string) {
	rr, err := grpcrr.Open(filepath.FromSlash(file))
	if err != nil {
		t.Fatalf("grpcrr.Open: %v", err)
	}
	if rr.Recording() {
		projectID = gcpconfig.MustProject(t)
		rr.SetInitial([]byte(projectID))
		return rr, projectID
	}
	return rr, string(rr.Initial())
}

func panics(f func()) (b bool) {
	defer func() {
		if recover() != nil {
			b = true
		}
	}()
	f()
	return false
}

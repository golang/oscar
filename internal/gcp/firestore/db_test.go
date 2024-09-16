// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package firestore

import (
	"context"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/oscar/internal/gcp/gcpconfig"
	"golang.org/x/oscar/internal/gcp/grpcrr"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

const firestoreTestDatabase = "test"

func TestDB(t *testing.T) {
	rr, project := openRR(t, "testdata/db.grpcrr")
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
	t.Logf("test DeleteRange")
	b := db.Batch()
	b.DeleteRange([]byte("a"), []byte("b"))
	applied := b.MaybeApply()
	if !applied {
		t.Error("MaybeApply with DeleteRange did not apply")
	}
}

// TestDBLimit checks that [DB.Scan] properly restarts from query limits (see docLimit).
func TestDBLimit(t *testing.T) {
	// Re-record with
	//	OSCAR_PROJECT=oscar-go-1 go test -v -run=TestDBLimit -grpcrecord=/dblimit
	rr, project := openRR(t, "testdata/dblimit.grpcrr")
	ctx := context.Background()

	db, err := NewDB(ctx, testutil.Slogger(t), project, firestoreTestDatabase, rr.ClientOptions()...)
	if err != nil {
		t.Fatal(err)
	}
	// Use a fixed UID for deterministic record/replay.
	db.uid = 1
	defer db.Close()

	b := db.Batch()

	db.fstore.docQueryLimit = 5
	N := (2 * db.fstore.docQueryLimit) + 1 // ensure we make a few iterations
	limitKey := func(n int) string {
		return fmt.Sprintf("limit.%09d", n)
	}
	for i := range N {
		b.Set([]byte(limitKey(i)), []byte("A"))
		b.MaybeApply()
	}
	b.Apply()
	n := 0
	for k := range db.Scan([]byte(limitKey(0)), []byte(limitKey(N-1))) {
		if string(k) != limitKey(n) {
			t.Fatalf("limit read #%d: have %q want %q", n, k, limitKey(n))
		}
		n++
	}
	if n != N {
		t.Errorf("limit reads: scanned %d keys, want %d", n, N)
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
		// Wait for lockRenew * 5 to make sure multiple renewals work.
		// (Before we added lock.Nonce, the second renewal failed.)
		db.Lock(name)
		time.Sleep(lockRenew * 5)
		db.Unlock(name)

		// Lock will be re-acquired with no errors.
		db.Lock(name)
		time.Sleep(lockRenew * 5)
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

	t.Run("concurrent", func(t *testing.T) {
		db, name := newTestDB(t, projectID)
		db2, _ := newTestDB(t, projectID)

		var wg sync.WaitGroup
		for range 5 {
			wg.Add(1)
			go func() {
				db.Lock(name)
				time.Sleep(time.Millisecond)
				db.Unlock(name)
				wg.Done()
			}()

			wg.Add(1)
			go func() {
				db2.Lock(name)
				time.Sleep(time.Millisecond)
				db2.Unlock(name)
				wg.Done()
			}()
		}

		wg.Wait()
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

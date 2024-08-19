// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package firestore

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
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

func TestDBSlow(t *testing.T) {
	// Re-record with
	//	OSCAR_PROJECT=oscar-go-1 go test -v -timeout=1h -run=TestDBSlow -grpcrecord=/dbslow
	rr, project := openRR(t, "testdata/dbslow.grpcrr.gz")
	ctx := context.Background()

	db, err := NewDB(ctx, testutil.Slogger(t), project, firestoreTestDatabase, rr.ClientOptions()...)
	if err != nil {
		t.Fatal(err)
	}
	// Use a fixed UID for deterministic record/replay.
	db.uid = 1
	defer db.Close()

	// Test that Scan does not die after 60 seconds.
	// Scan sends back keys in batches, so we have to
	// write a lot of keys to make sure we cross into
	// two batches.
	t.Logf("test slow scan")
	b := db.Batch()
	const slowN = 400
	slowKey := func(n int) string {
		return fmt.Sprintf("slow2.%09d", n)
	}
	for i := range slowN {
		b.Set([]byte(slowKey(i)), bytes.Repeat([]byte("A"), 100000))
		b.MaybeApply()
	}
	b.Apply()
	n := 0
	for k := range db.Scan([]byte(slowKey(0)), []byte(slowKey(slowN-1))) {
		if string(k) != slowKey(n) {
			t.Fatalf("slow read #%d: have %q want %q", n, k, slowKey(n))
		}
		if rr.Recording() && n%100 == 0 {
			t.Logf("scan %s; sleep", k)
			time.Sleep(90 * time.Second)
		}
		n++
	}
	if n != slowN {
		t.Errorf("slow reads: scanned %d keys, want %d", n, slowN)
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
	check := testutil.Checker(t)

	// If target is a gzip file, decompress (if it exists) into a temp file,
	// and recompress when test is done if modified.
	if strings.HasSuffix(file, ".gz") {
		// Decompress into temp file if it exists.
		f, err := os.CreateTemp(t.TempDir(), strings.TrimSuffix(filepath.Base(file), "gz"))
		check(err)

		gzfile := file
		tmpfile := f.Name()
		file = tmpfile

		var before fs.FileInfo
		if zf, err := os.Open(gzfile); err != nil {
			// File does not exist; remove temp file to avoid "empty replay file" error.
			f.Close()
			os.Remove(tmpfile)
		} else {
			zr, err := gzip.NewReader(zf)
			check(err)
			_, err = io.Copy(f, zr)
			check(err)
			zr.Close()
			zf.Close()
			before, err = f.Stat()
			check(err)
			f.Close()
		}

		// Recompress back during test cleanup.
		t.Cleanup(func() {
			f, err := os.Open(tmpfile)
			check(err)
			defer f.Close()
			after, err := f.Stat()
			check(err)
			if before != nil && after.ModTime() == before.ModTime() && after.Size() == before.Size() {
				// Trace not modified, must have been replaying.
				return
			}

			// Recompress trace into gzfile.
			zf, err := os.Create(gzfile)
			check(err)
			zw, err := gzip.NewWriterLevel(zf, gzip.BestCompression)
			check(err)
			_, err = io.Copy(zw, f)
			check(err)
			check(zw.Close())
			check(zf.Close())
		})
	}

	rr, err := grpcrr.Open(file)
	check(err)

	t.Cleanup(func() {
		if err := rr.Close(); err != nil {
			t.Fatal(err)
		}
	})

	if rr.Recording() {
		projectID = gcpconfig.MustProject(t)
		rr.SetInitial([]byte(projectID))
		return rr, projectID
	}
	return rr, string(rr.Initial())
}

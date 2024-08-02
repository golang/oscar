// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package firestore

import (
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oscar/internal/grpcrr"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

var (
	project  = flag.String("project", "", "project ID for testing")
	database = flag.String("database", "", "Firestore database for testing")
)

func TestDB(t *testing.T) {
	rr, fsProject, fsDatabase := openRR(t, "testdata/db.grpcrr")
	defer func() {
		if err := rr.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	ctx := context.Background()

	db, err := NewDB(ctx, testutil.Slogger(t), fsProject, fsDatabase, rr.ClientOptions()...)
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
	if *project == "" {
		t.Skip("missing -project")
	}
	ctx := context.Background()
	db, err := NewDB(ctx, testutil.Slogger(t), *project, *database, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	defer func(lt time.Duration) {
		lockTimeout = lt
	}(lockTimeout)
	lockTimeout = 2 * time.Second

	t.Run("basic", func(t *testing.T) {
		storage.TestDBLock(t, db)
	})

	t.Run("timeout", func(t *testing.T) {
		c := make(chan struct{})

		go func() {
			db.Lock("L")
			// The second Lock should wait until the first Lock times
			// out, then it should succeed.
			db.Lock("L")
			db.Unlock("L")
			close(c)
		}()
		select {
		case <-c:
			// Success.
		case <-time.After(2 * lockTimeout):
			t.Fatal("lock timeout didn't happen in time")
		}
	})
	t.Run("owner", func(t *testing.T) {
		// When run frequently, this test causes contention on the lock name.
		// So pick a random name.
		name := fmt.Sprintf("M%d", rand.IntN(10))
		db.deleteLock(nil, name) // Ensure the lock is not present.
		db2, err := NewDB(ctx, testutil.Slogger(t), *project, *database, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer db2.Close()

		func() {
			defer func() { recover() }()
			db.Lock(name)
			db2.Unlock(name)
			t.Error("unlock wrong owner did not panic")
		}()
	})
}

func TestErrors(t *testing.T) {
	if !panics(func() {
		var b batch
		b.set("x", nil, 1)
	}) {
		t.Error("batch.set does not panic on nil value")
	}
}

func openRR(t *testing.T, file string) (_ *grpcrr.RecordReplay, fsProject, fsDatabase string) {
	rr, err := grpcrr.Open(filepath.FromSlash(file))
	if err != nil {
		t.Fatalf("grpcrr.Open: %v", err)
	}
	if rr.Recording() {
		if *project == "" {
			t.Fatal("recording requires -project")
		}
		// The -database flag can be omitted. We'll use the default one.
		rr.SetInitial([]byte(*project + "," + *database))
		return rr, *project, *database
	}
	// Allow -project and -database on replay because other tests might need them.
	var found bool
	fsProject, fsDatabase, found = strings.Cut(string(rr.Initial()), ",")
	if !found {
		t.Fatalf("%s: bad initial state", file)
	}
	return rr, fsProject, fsDatabase
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

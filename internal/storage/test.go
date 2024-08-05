// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package storage

import (
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"rsc.io/ordered"
)

// TestDB runs basic tests on db.
// It should be empty when TestDB is called.
// To run tests on Lock and Unlock, also call [TestDBLock].
func TestDB(t *testing.T, db DB) {
	db.Set([]byte("key"), []byte("value"))
	if val, ok := db.Get([]byte("key")); string(val) != "value" || ok != true {
		// unreachable except for bad db
		t.Fatalf("Get(key) = %q, %v, want %q, true", val, ok, "value")
	}
	if val, ok := db.Get([]byte("missing")); val != nil || ok != false {
		// unreachable except for bad db
		t.Fatalf("Get(missing) = %v, %v, want nil, false", val, ok)
	}

	db.Delete([]byte("key"))
	if val, ok := db.Get([]byte("key")); val != nil || ok != false {
		// unreachable except for bad db
		t.Fatalf("Get(key) after delete = %v, %v, want nil, false", val, ok)
	}

	b := db.Batch()
	for i := range 10 {
		b.Set(ordered.Encode(i), []byte(fmt.Sprint(i)))
		b.MaybeApply()
	}
	b.Apply()

	collect := func(min, max, stop int) []int {
		t.Helper()
		var list []int
		for key, val := range db.Scan(ordered.Encode(min), ordered.Encode(max)) {
			var i int
			if err := ordered.Decode(key, &i); err != nil {
				// unreachable except for bad db
				t.Fatalf("db.Scan malformed key %v", Fmt(key))
			}
			if sv, want := string(val()), fmt.Sprint(i); sv != want {
				// unreachable except for bad db
				t.Fatalf("db.Scan key %v val=%q, want %q", i, sv, want)
			}
			list = append(list, i)
			if i == stop {
				break
			}
		}
		return list
	}

	if scan, want := collect(3, 6, -1), []int{3, 4, 5, 6}; !slices.Equal(scan, want) {
		// unreachable except for bad db
		t.Fatalf("Scan(3, 6) = %v, want %v", scan, want)
	}

	if scan, want := collect(3, 6, 5), []int{3, 4, 5}; !slices.Equal(scan, want) {
		// unreachable except for bad db
		t.Fatalf("Scan(3, 6) with break at 5 = %v, want %v", scan, want)
	}

	db.DeleteRange(ordered.Encode(4), ordered.Encode(7))
	if scan, want := collect(-1, 11, -1), []int{0, 1, 2, 3, 8, 9}; !slices.Equal(scan, want) {
		// unreachable except for bad db
		t.Fatalf("Scan(-1, 11) after Delete(4, 7) = %v, want %v", scan, want)
	}

	b = db.Batch()
	for i := range 5 {
		b.Delete(ordered.Encode(i))
		b.Set(ordered.Encode(2*i), []byte(fmt.Sprint(2*i)))
	}
	b.DeleteRange(ordered.Encode(0), ordered.Encode(0))
	b.Apply()
	if scan, want := collect(-1, 11, -1), []int{6, 8, 9}; !slices.Equal(scan, want) {
		// unreachable except for bad db
		t.Fatalf("Scan(-1, 11) after batch Delete+Set = %v, want %v", scan, want)
	}

	// Check that batch.Apply clears the batch.
	k := ordered.Encode("a")
	b = db.Batch()
	b.Set(k, []byte{0})
	b.Apply()
	db.Delete(k)
	b.Apply() // should be a no-op
	if _, ok := db.Get(k); ok {
		t.Fatalf("empty Apply should be no-op, but got previous value")
	}

	// Can't test much, but check that it doesn't crash.
	db.Flush()
}

type locker interface {
	Lock(string)
	Unlock(string)
}

// TestDBLock verifies that Lock behaves correctly.
// It is separate from [TestDB] because it can't be used
// with a recorder/replayer, thanks to its sensitivity
// to time.
func TestDBLock(t *testing.T, db locker) {
	db.Lock("abc")
	c := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		db.Lock("abc")
		close(c)
		db.Unlock("abc")
		wg.Done()
	}()

	// The db.Lock in the goroutine should block, since the lock is already held.
	select {
	case <-c:
		t.Fatal("Lock did not wait")
	case <-time.After(1 * time.Second):
	}

	db.Unlock("abc")

	// Now the db.Lock in the goroutine should return.
	<-c
	wg.Wait()

	func() {
		defer func() {
			recover()
		}()
		db.Unlock("def")
		t.Errorf("Unlock never-locked key did not panic")
	}()

}

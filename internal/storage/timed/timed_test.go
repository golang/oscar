// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package timed

import (
	"slices"
	"strings"
	"testing"

	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func Test(t *testing.T) {
	db := storage.MemDB()
	b := db.Batch()

	Set(db, b, "kind", []byte("key"), []byte("val"))
	if e, ok := Get(db, "kind", []byte("key")); e != nil || ok != false {
		t.Errorf("Set wrote to db instead of b: Get = %v, %v, want nil, false", e, ok)
	}
	b.Apply()
	if e, ok := Get(db, "kind", []byte("key")); !ok || e == nil || e.Kind != "kind" || string(e.Key) != "key" || string(e.Val) != "val" || e.ModTime == 0 {
		t.Errorf("Get after Set = %+v, %v, want {>0, kind, key, val}, true", e, ok)
	}

	Delete(db, b, "kind", []byte("missing"))
	b.Apply()
	if e, ok := Get(db, "kind", []byte("key")); !ok || e == nil || e.Kind != "kind" || string(e.Key) != "key" || string(e.Val) != "val" || e.ModTime == 0 {
		t.Errorf("Get after Delete = %+v, %v, want {>0, kind, key, val}, true", e, ok)
	}

	Delete(db, b, "kind", []byte("key"))
	b.Apply()
	if e, ok := Get(db, "kind", []byte("key")); e != nil || ok != false {
		t.Errorf("Delete didn't delete key: Get = %v, %v, want nil, false", e, ok)
	}

	var keys []string
	var last DBTime
	do := func(e *Entry) {
		t.Helper()
		if last != -1 {
			if e.ModTime <= last {
				t.Fatalf("%+v: ModTime %v <= last %v", e, e.ModTime, last)
			}
			last = e.ModTime
		}
		if string(e.Kind) != "kind" {
			t.Fatalf("%+v: Kind=%q, want %q", e, e.Kind, "kind")
		}
		key := string(e.Key)
		if !strings.HasPrefix(key, "k") {
			t.Fatalf("%+v: Key=%q, want k prefix", e, e.Key)
		}
		if want := "v" + key[1:]; string(e.Val) != want {
			t.Fatalf("%+v: Val=%q, want %q", e, e.Val, want)
		}
		keys = append(keys, key)
	}

	Set(db, b, "kind", []byte("k1"), []byte("v1"))
	Set(db, b, "kind", []byte("k3"), []byte("v3"))
	Set(db, b, "kind", []byte("k2"), []byte("v2"))
	b.Apply()

	// Basic iteration.
	last = -1
	keys = nil
	for e := range Scan(db, "kind", nil, []byte("\xff")) {
		do(e)
	}
	if want := []string{"k1", "k2", "k3"}; !slices.Equal(keys, want) {
		t.Errorf("Scan() = %v, want %v", keys, want)
	}

	keys = nil
	for e := range Scan(db, "kind", []byte("k1x"), []byte("k2z")) {
		do(e)
	}
	if want := []string{"k2"}; !slices.Equal(keys, want) {
		t.Errorf("Scan(k1x, k2z) = %v, want %v", keys, want)
	}

	keys = nil
	for e := range Scan(db, "kind", []byte("k2"), []byte("\xff")) {
		do(e)
	}
	if want := []string{"k2", "k3"}; !slices.Equal(keys, want) {
		t.Errorf("Scan(k2) = %v, want %v", keys, want)
	}

	keys = nil
	for e := range Scan(db, "kind", []byte("k2"), []byte("\xff")) {
		do(e)
		break
	}
	if want := []string{"k2"}; !slices.Equal(keys, want) {
		t.Errorf("Scan(k2) with break = %v, want %v", keys, want)
	}

	// Timed iteration.
	last = 0
	keys = nil
	for e := range ScanAfter(db, "kind", 0, nil) {
		do(e)
	}
	if want := []string{"k1", "k3", "k2"}; !slices.Equal(keys, want) {
		t.Errorf("ScanAfter(0) = %v, want %v", keys, want)
	}
	t123 := last

	// Watcher.
	last = 0
	keys = nil
	w := NewWatcher(db, "name", "kind", func(e *Entry) *Entry { return e })
	if latest, want := w.Latest(), DBTime(0); latest != want {
		t.Errorf("Watcher.Latest() = %d, want %d", latest, want)
	}

	for e := range w.Recent() {
		do(e)
		w.MarkOld(e.ModTime)
		w.MarkOld(e.ModTime - 1) // no-op
	}
	if want := []string{"k1", "k3", "k2"}; !slices.Equal(keys, want) {
		t.Errorf("Watcher.Recent() = %v, want %v", keys, want)
	}
	if got := w.Latest(); got != last {
		t.Errorf("Watcher.Latest() = %d, want %d", got, last)
	}

	// Timed iteration with break.
	last = 0
	keys = nil
	for e := range ScanAfter(db, "kind", 0, nil) {
		do(e)
		break
	}
	if want := []string{"k1"}; !slices.Equal(keys, want) {
		t.Errorf("ScanAfter(0) with break = %v, want %v", keys, want)
	}

	// Incremental iteration
	Set(db, b, "kind", []byte("k5"), []byte("v5"))
	Set(db, b, "kind", []byte("k4"), []byte("v4"))
	Set(db, b, "kind", []byte("k2"), []byte("v2"))
	b.Apply()

	// Check full scan.
	last = 0
	keys = nil
	for e := range ScanAfter(db, "kind", 0, nil) {
		do(e)
	}
	if want := []string{"k1", "k3", "k5", "k4", "k2"}; !slices.Equal(keys, want) {
		t.Errorf("ScanAfter(0) = %v, want %v", keys, want)
	}

	// Check incremental scan.
	last = 0
	keys = nil
	for e := range ScanAfter(db, "kind", t123, nil) {
		do(e)
	}
	if want := []string{"k5", "k4", "k2"}; !slices.Equal(keys, want) {
		t.Errorf("ScanAfter(t123) = %v, want %v", keys, want)
	}

	// Full (new) watcher.
	last = 0
	keys = nil
	w = NewWatcher(db, "name2", "kind", func(e *Entry) *Entry { return e })
	for e := range w.Recent() {
		do(e)
	}
	if want := []string{"k1", "k3", "k5", "k4", "k2"}; !slices.Equal(keys, want) {
		t.Errorf("Watcher.Recent() full = %v, want %v", keys, want)
	}

	// Watcher with break
	last = 0
	keys = nil
	w = NewWatcher(db, "name2", "kind", func(e *Entry) *Entry { return e })
	for e := range w.Recent() {
		do(e)
		break
	}
	if want := []string{"k1"}; !slices.Equal(keys, want) {
		t.Errorf("Watcher.Recent() full = %v, want %v", keys, want)
	}

	// Incremental (old) watcher.
	last = 0
	keys = nil
	w = NewWatcher(db, "name", "kind", func(e *Entry) *Entry { return e })
	for e := range w.Recent() {
		do(e)
	}
	if want := []string{"k5", "k4", "k2"}; !slices.Equal(keys, want) {
		t.Errorf("Watcher.Recent() incremental = %v, want %v", keys, want)
	}

	// Restart incremental watcher.
	last = 0
	keys = nil
	w.Restart()
	for e := range w.Recent() {
		do(e)
	}
	if want := []string{"k1", "k3", "k5", "k4", "k2"}; !slices.Equal(keys, want) {
		t.Errorf("Watcher.Recent() after Reset = %v, want %v", keys, want)
	}

	// Filtered scan.
	last = 0
	keys = nil
	filter := func(key []byte) bool { return strings.HasSuffix(string(key), "3") }
	for e := range ScanAfter(db, "kind", 0, filter) {
		do(e)
	}
	if want := []string{"k3"}; !slices.Equal(keys, want) {
		t.Errorf("ScanAfter(0, suffix3) = %v, want %v", keys, want)
	}

	// Accidentally doing multiple Sets of a single key
	// will leave behind a stale timestamp record.
	Set(db, b, "kind", []byte("k3"), []byte("v3"))
	Set(db, b, "kind", []byte("k3"), []byte("v3"))
	b.Apply()

	// Stale timestamp should not result in multiple k3 visits.
	last = 0
	keys = nil
	for e := range ScanAfter(db, "kind", 0, nil) {
		do(e)
	}
	if want := []string{"k1", "k5", "k4", "k2", "k3"}; !slices.Equal(keys, want) {
		t.Errorf("ScanAfter(0) = %v, want %v", keys, want)
	}

	// Deleting k3 now will still leave the stale timestamp record.
	// Make sure it is ignored and doesn't cause a lookup crash.
	Delete(db, b, "kind", []byte("k3"))
	b.Apply()

	// Stale timestamp should not crash on k3.
	last = 0
	keys = nil
	for e := range ScanAfter(db, "kind", 0, nil) {
		do(e)
	}
	if want := []string{"k1", "k5", "k4", "k2"}; !slices.Equal(keys, want) {
		t.Errorf("ScanAfter(0) = %v, want %v", keys, want)
	}

	// Range deletion.
	DeleteRange(db, b, "kind", []byte("k1z"), []byte("k33"))
	b.Apply()

	last = -1
	keys = nil
	for e := range Scan(db, "kind", nil, []byte("\xff")) {
		do(e)
	}
	if want := []string{"k1", "k4", "k5"}; !slices.Equal(keys, want) {
		t.Errorf("Scan() after DeleteRange = %v, want %v", keys, want)
	}

	last = 0
	keys = nil
	for e := range ScanAfter(db, "kind", 0, nil) {
		do(e)
	}
	if want := []string{"k1", "k5", "k4"}; !slices.Equal(keys, want) {
		t.Errorf("ScanAfter(0) after DeleteRange = %v, want %v", keys, want)
	}

	Set(db, b, "kind", []byte("k2"), []byte("v2"))
	b.Apply()
}

func TestLocking(t *testing.T) {
	db := storage.MemDB()
	b := db.Batch()
	Set(db, b, "kind", []byte("key"), []byte("val"))
	b.Apply()

	w := NewWatcher(db, "name", "kind", func(e *Entry) *Entry { return e })

	w.lock()
	testutil.StopPanic(func() {
		w.lock()
		t.Fatalf("second w.lock did not panic")
	})

	w.unlock()
	testutil.StopPanic(func() {
		w.unlock()
		t.Fatalf("second w.unlock did not panic")
	})

	testutil.StopPanic(func() {
		w.MarkOld(0)
		t.Fatalf("MarkOld outside iteration did not panic")
	})

	did := false
	for range w.Recent() {
		did = true
		testutil.StopPanic(func() {
			w.Restart()
			t.Fatalf("Restart inside iteration did not panic")
		})

		testutil.StopPanic(func() {
			for range w.Recent() {
			}
			t.Fatalf("iteration inside iteration did not panic")
		})
	}
	if !did {
		t.Fatalf("range over Recent did not find any entries")
	}
}

func TestNow(t *testing.T) {
	t1 := now()
	for range 1000 {
		t2 := now()
		if t2 <= t1 {
			t.Errorf("now(), now() = %d, %d (out of order)", t1, t2)
		}
		t1 = t2
	}
}

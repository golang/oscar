// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package storage

import (
	"bytes"
	"slices"
	"testing"

	"rsc.io/ordered"
)

func TestOverlayDB(t *testing.T) {
	o := func(n int) []byte { return ordered.Encode(n) }

	newbase := func() DB {
		db := MemDB()
		db.Set(o(0), o(0))
		db.Set(o(9), o(9))
		return db
	}

	for _, test := range []struct {
		name string
		ops  func(DB)
	}{
		{
			"set3",
			func(db DB) { db.Set(o(3), o(3)) },
		},
		{
			"del9",
			func(db DB) { db.Delete(o(9)) },
		},
		{
			"del9set9",
			func(db DB) { db.Delete(o(9)); db.Set(o(9), o(4)) },
		},
		{
			"del5set3",
			func(db DB) { db.Delete(o(5)); db.Set(o(3), o(3)) },
		},
		{
			"get9",
			func(db DB) {
				v, ok := db.Get(o(9))
				if !ok || !bytes.Equal(v, o(9)) {
					t.Fatal("bad Get")
				}
			},
		},
		{
			"set9get9",
			func(db DB) {
				db.Set(o(9), o(1))
				v, ok := db.Get(o(9))
				if !ok || !bytes.Equal(v, o(1)) {
					t.Fatal("bad Get")
				}
			},
		},
		{
			"del9get9",
			func(db DB) {
				db.Delete(o(9))
				if _, ok := db.Get(o(9)); ok {
					t.Fatal("bad Get")
				}
			},
		},
		{
			"batch",
			func(db DB) {
				b := db.Batch()
				b.Set(o(1), o(1))
				b.Set(o(2), o(2))
				b.Delete(o(9))
				b.Set(o(9), o(9))
				b.DeleteRange(o(2), o(6))
				b.Apply()
			},
		},
		{
			"delrange",
			func(db DB) {
				for i := range 9 {
					db.Set(o(i), o(i))
				}
				db.DeleteRange(o(3), o(9))
				db.Set(o(3), o(3))
			},
		},
		{
			"delrange2",
			func(db DB) {
				for i := range 20 {
					db.Set(o(i), o(i))
				}
				db.DeleteRange(o(3), o(9))
				db.DeleteRange(o(8), o(12))
				db.DeleteRange(o(18), o(22))
				for _, k := range []int{4, 12, 15, 18, 23} {
					db.Set(o(k), o(k))
				}
				db.Delete(o(4))
				db.Delete(o(15))
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Run the operations on an overlay DB.
			base := newbase()
			over := MemDB()
			gdb := NewOverlayDB(over, base)
			test.ops(gdb)

			// Run the operations directly on the base DB.
			wdb := newbase()
			test.ops(wdb)
			// The overlay DB should behave exactly like an ordinary DB.
			got := items(gdb)
			want := items(wdb)
			if !slices.EqualFunc(got, want, item.Equal) {
				t.Errorf("\ngot  %v\nwant %v", got, want)
			}

			// The overlay DB should not change its base.
			bgot := items(base)
			if !slices.EqualFunc(bgot, items(newbase()), item.Equal) {
				t.Errorf("base changed: %v", bgot)
			}
		})
	}
}

type item struct {
	key, val []byte
}

func (i1 item) Equal(i2 item) bool {
	return bytes.Equal(i1.key, i2.key) && bytes.Equal(i1.val, i2.val)
}

func items(db DB) []item {
	var items []item
	for k, vf := range db.Scan(nil, ordered.Encode(ordered.Inf)) {
		var prefix string
		if _, err := ordered.DecodePrefix(k, &prefix); err == nil && prefix == overlayPrefix {
			continue
		}
		items = append(items, item{k, vf()})
	}
	return items
}

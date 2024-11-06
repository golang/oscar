// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package storage

import (
	"bytes"
	"slices"
	"testing"
)

func TestOverlayDB(t *testing.T) {
	bs := func(n byte) []byte { return []byte{n} }

	newbase := func() DB {
		db := MemDB()
		db.Set(bs(0), bs(0))
		db.Set(bs(9), bs(9))
		return db
	}

	for _, test := range []struct {
		name string
		ops  func(DB)
	}{
		{
			"set3",
			func(db DB) { db.Set(bs(3), bs(3)) },
		},
		{
			"del9",
			func(db DB) { db.Delete(bs(9)) },
		},
		{
			"del9set9",
			func(db DB) { db.Delete(bs(9)); db.Set(bs(9), bs(4)) },
		},
		{
			"del5set3",
			func(db DB) { db.Delete(bs(5)); db.Set(bs(3), bs(3)) },
		},
		{
			"get9",
			func(db DB) {
				v, ok := db.Get(bs(9))
				if !ok || !bytes.Equal(v, bs(9)) {
					t.Fatal("bad Get")
				}
			},
		},
		{
			"set9get9",
			func(db DB) {
				db.Set(bs(9), bs(1))
				v, ok := db.Get(bs(9))
				if !ok || !bytes.Equal(v, bs(1)) {
					t.Fatal("bad Get")
				}
			},
		},
		{
			"del9get9",
			func(db DB) {
				db.Delete(bs(9))
				if _, ok := db.Get(bs(9)); ok {
					t.Fatal("bad Get")
				}
			},
		},
		{
			"batch",
			func(db DB) {
				b := db.Batch()
				b.Set(bs(1), bs(1))
				b.Set(bs(2), bs(2))
				b.Delete(bs(9))
				b.Set(bs(9), bs(9))
				b.DeleteRange(bs(2), bs(6))
				b.Apply()
			},
		},
		{
			"delrange",
			func(db DB) {
				for i := range byte(9) {
					db.Set(bs(i), bs(i))
				}
				db.DeleteRange(bs(3), bs(9))
				db.Set(bs(3), bs(3))
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// The overlay DB should behave exactly like an ordinary DB.
			base := newbase()
			gdb := NewOverlayDB(base)
			test.ops(gdb)
			wdb := newbase()
			test.ops(wdb)
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
	for k, vf := range db.Scan([]byte{0}, []byte{255}) {
		items = append(items, item{k, vf()})
	}
	return items
}

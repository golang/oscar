// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gerrit

import (
	"context"
	"maps"
	"slices"
	"testing"

	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestLoadTxtar(t *testing.T) {
	check := testutil.Checker(t)

	lg := testutil.Slogger(t)
	db := storage.MemDB()
	sdb := secret.Empty()
	c := New("gerrit-test", lg, db, sdb, nil)

	tc := c.Testing()
	check(tc.LoadTxtar("testdata/change.txt"))
	ch := tc.change(1)
	if ch == nil {
		t.Fatal("could not find loaded change")
	}

	tests := []changeTests{
		{
			"ChangeLabels",
			wa(c.ChangeLabels),
			map[string]string{
				"Hold": "put change on hold",
			},
			func(got, want any) bool {
				g := got.(map[string]*LabelInfo)
				w := want.(map[string]string)
				m := make(map[string]string)
				for k, l := range g {
					m[k] = l.Description
				}
				return maps.Equal(w, m)
			},
		},
		{
			"ChangeOwner",
			wa(c.ChangeOwner),
			"gopher@golang.org",
			func(got, want any) bool {
				return got.(*AccountInfo).Email == want
			},
		},
		{
			"ChangeDescription",
			wa(c.ChangeDescription),
			"gerrit: test change",
			nil,
		},
		{
			"ChangeHashtags",
			wa(c.ChangeHashtags),
			[]string{"tag1", "tag2"},
			func(got, want any) bool {
				g := got.([]string)
				w := want.([]string)
				return slices.Equal(g, w)
			},
		},
		{
			"ChangeMessages",
			wa(c.ChangeMessages),
			[]string{
				"message 1",
				"message 2",
			},
			func(got, want any) bool {
				g := got.([]ChangeMessageInfo)
				w := want.([]string)
				for i, m := range g {
					if m.Message != w[i] {
						return false
					}
				}
				return true
			},
		},
	}

	testChangeTests(t, ch, tests)
}

func TestTestingChanges(t *testing.T) {
	check := testutil.Checker(t)
	ctx := context.Background()

	tc := TestingClient{}
	tc.queryLimit = 1000 // grab everything in one batch
	check(tc.LoadTxtar("testdata/uniquetimes.txt"))

	cnt := 0
	// There should be six changes matching the criteria, but skip the first one.
	for _, err := range tc.changes(ctx, "test", "2020-03-01 10:10:10.00000000", "2020-08-01 10:10:10.00000000", 1) {
		check(err)
		cnt++
	}

	if cnt != 5 {
		t.Errorf("want 5 changes; got %d", cnt)
	}
}

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

	ctx := context.Background()
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
				g := got.([]*ChangeMessageInfo)
				w := want.([]string)
				for i, m := range g {
					if m.Message != w[i] {
						return false
					}
				}
				return true
			},
		},
		{
			"ChangeReviewers",
			wa(c.ChangeReviewers),
			[]string{"gopher@golang.org"},
			func(got, want any) bool {
				g := got.([]*AccountInfo)
				w := want.([]string)
				ge := make([]string, 0, len(g))
				for _, a := range g {
					ge = append(ge, a.Email)
				}
				return slices.Equal(ge, w)
			},
		},
		{
			"ChangeMergeable",
			func(ch *Change) any {
				return c.ChangeMergeable(ctx, ch)
			},
			false,
			nil,
		},
	}

	testChangeTests(t, ch, tests)

	want := "comment 1"
	if got := tc.comments[1][0].Message; got != want {
		t.Errorf("change #1: got comment %q, want %q", got, want)
	}
}

func TestTestingChanges(t *testing.T) {
	check := testutil.Checker(t)
	ctx := context.Background()

	c := New("gerrit-test", nil, nil, nil, nil)
	tc := c.Testing()
	tc.setLimit(1000) // grab everything in one batch
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

func TestTestingChangeNumbers(t *testing.T) {
	check := testutil.Checker(t)

	lg := testutil.Slogger(t)
	db := storage.MemDB()
	sdb := secret.Empty()
	c := New("gerrit-test", lg, db, sdb, nil)

	tc := c.Testing()
	check(tc.LoadTxtar("testdata/uniquetimes.txt"))

	idx := 1
	for num, chfn := range c.ChangeNumbers("test") {
		if num != idx {
			t.Errorf("ChangeNumbers returned change %d, want %d", num, idx)
		}
		ch := chfn()
		if got := c.ChangeNumber(ch); got != idx {
			t.Errorf("ChangeNumbers returned change with number %d, want %d", got, idx)
		}
		idx++
	}
	if idx != 11 {
		t.Errorf("ChangeNumbers returned %d changes, want 10", idx-1)
	}
}

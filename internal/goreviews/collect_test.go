// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package goreviews

import (
	"context"
	"slices"
	"testing"

	"golang.org/x/oscar/internal/gerrit"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestCollectChanges(t *testing.T) {
	check := testutil.Checker(t)

	lg := testutil.Slogger(t)
	db := storage.MemDB()
	sdb := secret.Empty()
	gc := gerrit.New("goreviews-test", lg, db, sdb, nil)

	tc := gc.Testing()

	check(tc.LoadTxtar("testdata/changes.txt"))

	ctx := context.Background()
	cps, err := collectChanges(ctx, lg, gc, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}

	type changePredNames struct {
		changeID string
		preds    []string
	}

	var got []changePredNames
	for _, cp := range cps {
		cpn := changePredNames{
			changeID: cp.Change.ID(),
		}
		for _, pred := range cp.Predicates {
			cpn.preds = append(cpn.preds, pred.Name)
		}
		slices.Sort(cpn.preds)
		got = append(got, cpn)
	}

	want := []changePredNames{
		{
			changeID: "4",
			preds: []string{
				"authorMaintainer",
				"noMaintainerReviews",
			},
		},
		{
			changeID: "5",
			preds: []string{
				"authorReviewer",
				"noMaintainerReviews",
			},
		},
		{
			changeID: "17",
			preds: []string{
				"authorContributor",
				"noMaintainerReviews",
			},
		},
		{
			changeID: "20",
			preds: []string{
				"authorContributor",
				"noMaintainerReviews",
			},
		},
		{
			changeID: "1",
			preds:    []string{},
		},
		{
			changeID: "2",
			preds:    []string{},
		},
	}

	eqChangePredNames := func(a, b changePredNames) bool {
		return a.changeID == b.changeID && slices.Equal(a.preds, b.preds)
	}

	if !slices.EqualFunc(got, want, eqChangePredNames) {
		t.Errorf("collectChanges returned %v, want %v", got, want)
	}
}

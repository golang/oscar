// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package reviews

import (
	"context"
	"slices"
	"testing"

	"golang.org/x/oscar/internal/gerrit"
	"golang.org/x/oscar/internal/testutil"
)

func TestCollectChangePreds(t *testing.T) {
	gc := testGerritClient(t)
	testutil.Check(t, gc.Testing().LoadTxtar("testdata/gerritchange.txt"))

	// Fetch changes and convert to a sequence of gerrit.Change
	gchanges := func(yield func(*gerrit.Change) bool) {
		for _, changeFn := range gc.ChangeNumbers("test") {
			if !yield(changeFn()) {
				return
			}
		}
	}

	// Convert a iter.Seq[*GerritChange] into a iter.Seq[Change].
	changes := func(yield func(Change) bool) {
		for gc := range GerritChanges(gc, testAccounts(), gchanges) {
			if !yield(gc) {
				return
			}
		}
	}

	ctx := context.Background()
	cps := CollectChangePreds(ctx, testutil.Slogger(t), changes, Predicates(), Rejects())
	if len(cps) != 1 {
		t.Errorf("CollectChangePreds returned %d entries, want 1", len(cps))
	}
	if len(cps) == 0 {
		return
	}
	cp := cps[0]
	if got := cp.Change.ID(ctx); got != "1" {
		t.Errorf("CollectChangePreds returned change %s, want 1", got)
	}
	var got []string
	for _, s := range cp.Predicates {
		got = append(got, s.Name)
	}
	want := []string{"authorReviewer"}
	if !slices.Equal(got, want) {
		t.Errorf("CollectChangePreds returned change with predicates %v, want %v", got, want)
	}
}

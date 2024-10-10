// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package reviews

import (
	"context"
	"slices"
	"testing"

	"golang.org/x/oscar/internal/testutil"
)

func TestCollectChangePreds(t *testing.T) {
	gc := testGerritClient(t)
	testutil.Check(t, gc.Testing().LoadTxtar("testdata/gerritchange.txt"))

	ctx := context.Background()

	// Convert a iter.Seq[*GerritChange] into a iter.Seq[Change].
	changes := func(yield func(Change) bool) {
		for gc := range GerritChanges(ctx, gc, []string{"test"}, testAccounts()) {
			if !yield(gc) {
				return
			}
		}
	}

	cps := CollectChangePreds(ctx, testutil.Slogger(t), changes)
	if len(cps) != 1 {
		t.Errorf("CollectChangePreds returned %d entries, want 1", len(cps))
	}
	if len(cps) == 0 {
		return
	}
	cp := cps[0]
	if got := cp.Change.ID(); got != "1" {
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

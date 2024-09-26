// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gerrit

import (
	"context"
	"testing"

	"golang.org/x/oscar/internal/testutil"
)

func TestTestingChanges(t *testing.T) {
	check := testutil.Checker(t)
	ctx := context.Background()

	tc := TestingClient{}
	tc.queryLimit = 1000 // grab everything in one batch
	check(tc.LoadTxtar("testdata/uniquetimes.txt"))

	cnt := 0
	// There should be six changes matching the criteria, but skip the first one.
	for _, err := range tc.changes(ctx, "", "2020-03-01 10:10:10.00000000", "2020-08-01 10:10:10.00000000", 1) {
		check(err)
		cnt++
	}

	if cnt != 5 {
		t.Errorf("want 5 changes; got %d", cnt)
	}
}

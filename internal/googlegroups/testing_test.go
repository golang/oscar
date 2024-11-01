// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package googlegroups

import (
	"context"
	"testing"

	"golang.org/x/oscar/internal/testutil"
)

func TestLoadTxtar(t *testing.T) {
	check := testutil.Checker(t)

	c := New(nil, nil, nil, nil)

	tc := c.Testing()
	check(tc.LoadTxtar("testdata/convs.txt"))
	if len(tc.convs) != 3 {
		t.Errorf("want 3 conversations; got %d", len(tc.convs))
	}
}

func TestTestingChanges(t *testing.T) {
	check := testutil.Checker(t)
	ctx := context.Background()

	c := New(nil, nil, nil, nil)
	tc := c.Testing()
	tc.setLimit(1000) // grab everything in one batch
	check(tc.LoadTxtar("testdata/interrupt_convs.txt"))

	cnt := 0
	// There should be three conversations matching the criteria.
	for _, err := range tc.conversations(ctx, "test", "2024-10-18", "2024-10-20") {
		check(err)
		cnt++
	}

	if cnt != 3 {
		t.Errorf("want 3 conversations; got %d", cnt)
	}
}

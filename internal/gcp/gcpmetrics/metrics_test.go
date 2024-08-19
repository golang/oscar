// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcpmetrics

import (
	"context"
	"flag"
	"testing"

	"golang.org/x/oscar/internal/testutil"
)

var project = flag.String("project", "", "GCP project ID")

// This test checks that metrics are exported to the GCP monitoring API.
// (If we don't actually send the metrics, there is really nothing to test.)
func Test(t *testing.T) {
	if *project == "" {
		t.Skip("skipping without -project")
	}
	ctx := context.Background()

	mp, err := NewMeterProvider(ctx, testutil.Slogger(t), *project)
	if err != nil {
		t.Fatal(err)
	}
	meter := mp.Meter("test")
	c, err := meter.Int64Counter("test-counter")
	if err != nil {
		t.Fatal(err)
	}
	c.Add(ctx, 1)

	// Force an export even if the interval hasn't passed.
	if err := mp.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	if g, w := totalExports.Load(), int64(1); g != w {
		t.Errorf("total exports: got %d, want %d", g, w)
	}

	if g, w := failedExports.Load(), int64(0); g != w {
		t.Errorf("failed exports: got %d, want %d", g, w)
	}
}

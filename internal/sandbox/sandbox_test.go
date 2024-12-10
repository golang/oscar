// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sandbox

import (
	"testing"
)

func TestValidate(t *testing.T) {
	// Validate doesn't actually run the sandbox, so we can test it.
	sb := New("testdata/bundle")
	sb.Runsc = "/usr/local/bin/runsc"
	if err := sb.Validate(); err != nil {
		t.Fatal(err)
	}
}

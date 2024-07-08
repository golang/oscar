// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"testing"

	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestLoadTxtar(t *testing.T) {
	gh := New(testutil.Slogger(t), storage.MemDB(), nil, nil)
	testutil.Check(t, gh.Testing().LoadTxtar("../testdata/rsctmp.txt"))
}

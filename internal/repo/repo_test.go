// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package repo

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/oscar/internal/testutil"
)

// A git repo for testing.
const (
	oscarRepo = "https://go.googlesource.com/oscar"
	oscarRev  = "1a73f3fd3eff9030bb4f172acca1b901b455906e"
)

func TestClone(t *testing.T) {
	ctx := context.Background()
	lg := testutil.Slogger(t)

	clone := func(dir string) ([]byte, error) {
		err := os.MkdirAll(filepath.Join(dir, "gitdir"), 0o755)
		return nil, err
	}

	var se testutil.StubExecutor
	se.Add("git", []string{"clone", oscarRepo}, clone)

	r, err := Clone(ctx, lg, oscarRepo, &se)
	if err != nil {
		t.Fatal(err)
	}

	checkout := func(dir string) ([]byte, error) {
		return nil, nil
	}

	se.Add("git", []string{"checkout", oscarRev}, checkout)

	if err := r.Checkout(ctx, lg, oscarRev, &se); err != nil {
		t.Error(err)
	}

	r.Release()

	FreeAll()
}

func TestCloneNetwork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test that uses network in short mode")
	}

	ctx := context.Background()
	lg := testutil.Slogger(t)

	r, err := Clone(ctx, lg, oscarRepo, nil)
	if err != nil {
		t.Fatal(err)
	}

	gomodFile := filepath.Join(r.Dir(), "go.mod")
	if gomod, err := os.ReadFile(gomodFile); err != nil {
		t.Error(err)
	} else {
		module, _, _ := bytes.Cut(gomod, []byte{'\n'})
		const want = "golang.org/x/oscar"
		if !bytes.Contains(module, []byte(want)) {
			t.Errorf("go.mod first line = %q, want it to contain %q", module, want)
		}
	}

	if err := r.Checkout(ctx, lg, oscarRev, nil); err != nil {
		t.Error(err)
	} else {
		if _, err := os.Stat(gomodFile); err == nil {
			t.Errorf("go.mod file unexpectedly exists")
		} else if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("go.mod file error = %v, expected does-not-exist", err)
		}
	}

	r2, err := Clone(ctx, lg, oscarRepo, nil)
	if r2.Dir() == r.Dir() {
		t.Errorf("second Clone call using same directory %q", r.Dir())
	}

	d1 := r.Dir()
	d2 := r2.Dir()

	r.Release()
	r2.Release()

	r, err = Clone(ctx, lg, oscarRepo, nil)
	if err != nil {
		t.Fatal(err)
	}

	switch r.Dir() {
	case d1, d2:
	default:
		t.Errorf("Clone after Release = %q, want %q or %q", r.Dir(), d1, d2)
	}
	r.Release()

	FreeAll()

	if _, err := os.Stat(d1); err == nil {
		t.Errorf("directory %q unexpectedly exists", d1)
	}
	if _, err := os.Stat(d2); err == nil {
		t.Errorf("directory %q unexpectedly exists", d2)
	}
}

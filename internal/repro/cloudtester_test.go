// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package repro

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/oscar/internal/bisect"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/queue"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestCloudTester(t *testing.T) {
	ctx := context.Background()
	lg := testutil.Slogger(t)
	db := storage.MemDB()

	var bc *bisect.Client
	process := func(ctx context.Context, task queue.Task) error {
		url, err := url.Parse(task.Path() + "?" + task.Params())
		if err != nil {
			return err
		}
		return bc.Bisect(ctx, url.Query().Get("id"))
	}
	q := queue.NewInMemory(ctx, 1, process)

	bc = bisect.New(lg, db, q)
	tbc := bc.Testing()
	tbc.Output = ""

	var se testutil.StubExecutor

	// Set up enough of a Go repo to satisfy NewGoTester.
	clone := func(dir string) ([]byte, error) {
		const subdir = "go/src/internal/goversion"
		if err := os.MkdirAll(filepath.Join(dir, subdir), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(dir, subdir, "goversion.go"), []byte("const Version = 24"), 0o644); err != nil {
			return nil, err
		}
		return nil, nil
	}
	buildRuntester := func(dir string) ([]byte, error) {
		if err := os.WriteFile(filepath.Join(dir, "runtester"), nil, 0o755); err != nil {
			return nil, err
		}
		return nil, nil
	}
	modInit := func(dir string) ([]byte, error) {
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module gotester\ngo 1.20"), 0o644); err != nil {
			return nil, err
		}
		return nil, nil
	}
	modEdit := func(dir string) ([]byte, error) {
		return nil, nil
	}
	se.Add("git", []string{"clone", goGitRepo}, clone)
	se.Add("go", []string{"build", "runtester.go"}, buildRuntester)
	se.Add("go", []string{"mod", "init", "gotester"}, modInit)
	se.Add("go", []string{"mod", "edit", "-go=1.20"}, modEdit)

	ct, err := NewCloudTester(ctx, lg, db, nil, goGitRepo, bc, &se)
	if err != nil {
		t.Fatal(err)
	}

	importsFile := filepath.Join(ct.goTester.testDir(), "imports.go")
	goimports := func(dir string) ([]byte, error) {
		if err := os.WriteFile(importsFile, []byte(wantClean), 0o644); err != nil {
			return nil, err
		}
		return nil, nil
	}

	se.AddPath("goimports", "/usr/bin/goimports")
	se.Add("/usr/bin/goimports", []string{importsFile}, goimports)

	bodyClean, err := ct.Clean(ctx, body)
	if bodyClean != wantClean {
		t.Errorf("Clean(%q) = %q, want %q", body, bodyClean, wantClean)
	}
	if err != nil {
		t.Errorf("Clean(%q): %v", body, err)
	}

	pass, fail := ct.CleanVersions(ctx, "1.23", "1.24")
	if want := "release-branch.go1.23"; pass != want {
		t.Errorf("got pass %s, want %s", pass, want)
	}
	if want := "master"; fail != want {
		t.Errorf("got fail %s, want %s", fail, want)
	}

	checkout := func(string) ([]byte, error) {
		return nil, nil
	}
	runtester := func(string) ([]byte, error) {
		return nil, nil
	}
	se.Add("git", []string{"checkout", pass}, checkout)
	se.Add("git", []string{"checkout", fail}, checkout)
	se.Add(ct.goTester.runTester(), []string{filepath.Join(ct.goTester.testDir(), "body.go")}, runtester)

	if _, err = ct.Try(ctx, bodyClean, fail); err != nil {
		t.Errorf("Try(%s) failed: %v", fail, err)
	}
	if _, err = ct.Try(ctx, bodyClean, pass); err != nil {
		t.Errorf("Try(%s) failed: %v", pass, err)
	}

	issue := &github.Issue{
		Number: 70468,
		URL:    "https://go.dev/issue/70468",
	}
	if _, err := ct.Bisect(ctx, issue, bodyClean, pass, fail); err != nil {
		t.Errorf("Bisect failed: %v", err)
	}
}

const body = `func main() {
	fmt.Println("Hello, world")
}`

const wantClean = `//go run
package main

import (
	"fmt"
)

func main() {
	fmt.Println("Hello, world")
}`

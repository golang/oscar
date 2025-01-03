// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The test in this file enforces restrictions on package dependencies.

package oscar

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"golang.org/x/mod/module"
	"golang.org/x/tools/go/packages"
)

// A rule describes the allowed dependencies for one or more packages.
// The patterns in the rule are a subset of the patterns used by the go command.
// A pattern is either a literal import path, which matches only itself,
// or of the form "P/...", which matches P and any path with the prefix "P/".
// As a special case, the pattern "..." matches every path.
type rule struct {
	// A pattern describing the packages to which this rule applies.
	// Paths are relative to the module root (this directory).
	// For example, "internal/gaby".
	packages string

	// Patterns describing paths that the above packages can import.
	// A package to which this rule applies must only import a package
	// matched by one of these patterns.
	// These patterns describe full import paths, for example "rsc.io/...".
	// Standard library packages are always allowed.
	// A value of nil means only standard library packages are allowed.
	allow []string
}

// Packages that are always allowed.
var alwaysAllowed = []string{
	"golang.org/...",
	"google.golang.org/...",
	"github.com/golang/...",
	"github.com/google/...",
	"github.com/cockroachdb/pebble",
	"rsc.io/markdown",
	"rsc.io/omap",
	"rsc.io/ordered",
	"rsc.io/top",
	"github.com/shurcooL/githubv4",
	"github.com/shurcooL/graphql/...",
	"gopkg.in/yaml.v3",
}

var anything = []string{"..."}

// rules is the list of rules for this module.
// For each package, only the first matching rule is used.
var rules = []*rule{
	{
		packages: "internal/syncdb/...",
		allow:    anything,
	},
	{
		packages: "internal/devtools/...",
		allow:    anything,
	},
	{
		packages: "internal/gaby/...",
		allow:    anything,
	},
	{
		packages: "internal/gcp/...",
		allow:    anything,
	},
	{
		packages: "internal/pebble/...",
		allow:    anything,
	},
	{
		packages: "internal/dbspec/...",
		allow:    anything,
	},
	// The remaining packages under internal should not depend on GCP.
	{
		packages: "internal/...",
		allow:    alwaysAllowed,
	},
}

const pkgPathPrefix = "golang.org/x/oscar/"

type Package = packages.Package

func TestDependencies(t *testing.T) {
	for _, r := range rules {
		if err := r.check(); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &packages.Config{
		Mode:  packages.NeedName | packages.NeedImports | packages.NeedDeps,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, pkgPathPrefix+"...")
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			t.Errorf("package %s has errors: %v", pkg.PkgPath, pkg.Errors)
			continue
		}
		relativePkgPath := strings.TrimPrefix(pkg.PkgPath, pkgPathPrefix)
		var mr *rule
		for _, r := range rules {
			if matchPattern(r.packages, relativePkgPath) {
				mr = r
				break
			}
		}
		if mr == nil {
			t.Fatalf("no rule matches import path %q", relativePkgPath)
		}
		deps := dependencies(pkg)
		for _, d := range deps {
			if !mr.allowed(d) {
				t.Errorf("package %s depends on disallowed package %s", relativePkgPath, d)
			}
		}
	}
}

// dependencies returns all the package paths that pkgdepends on, recursively.
func dependencies(pkg *Package) []string {
	depset := map[string]bool{}

	var deps func(*Package, int)
	deps = func(p *Package, level int) {
		if inStdLib(p.PkgPath) || depset[p.PkgPath] {
			return
		}
		for _, ip := range p.Imports {
			deps(ip, level+1)
			depset[ip.PkgPath] = true
		}
	}

	deps(pkg, 1)
	return slices.Collect(maps.Keys(depset))
}

func (r *rule) allowed(pkg string) bool {
	// Standard library packages are always allowed.
	if inStdLib(pkg) {
		return true
	}
	for _, a := range r.allow {
		if matchPattern(a, pkg) {
			return true
		}
	}
	return false
}

func TestAllowed(t *testing.T) {
	r := &rule{packages: "...", allow: []string{"a.com/b/...", "x.org/y/z"}}
	if err := r.check(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		pkg  string
		want bool
	}{
		{"a.com/b", true},
		{"a.com/b/c", true},
		{"a.com/b/c/d/e", true},
		{"x.org/y", false},
		{"x.org/y/z", true},
		{"x.org/y/z/w", false},
		{"a.org/b/c", false},
	} {
		got := r.allowed(tc.pkg)
		if got != tc.want {
			t.Errorf("%q: got %t, want %t", tc.pkg, got, tc.want)
		}
	}
}

// inStdLib reports whether the given import path could be part of the Go standard library,
// by reporting whether the first component lacks a '.'.
func inStdLib(path string) bool {
	if i := strings.IndexByte(path, '/'); i != -1 {
		path = path[:i]
	}
	return !strings.Contains(path, ".")
}

// check validates a rule.
func (r *rule) check() error {
	if err := checkPattern(r.packages); err != nil {
		return err
	}
	for _, a := range r.allow {
		if err := checkPattern(a); err != nil {
			return err
		}
		if inStdLib(a) {
			return fmt.Errorf("%q matches standard library packages, which are always allowed", a)
		}
	}
	return nil
}

// checkPattern validates a pattern.
func checkPattern(pattern string) error {
	prefix, found := strings.CutSuffix(pattern, "...")
	if found {
		if prefix == "" {
			return nil
		}
		if prefix[len(prefix)-1] != '/' {
			return fmt.Errorf("invalid pattern %q: does not end in '/...'", pattern)
		}
		prefix = prefix[:len(prefix)-1]
	}
	if strings.Contains(prefix, "...") {
		return fmt.Errorf("invalid pattern %q: '...' not at the end", pattern)
	} else if prefix == "" {
		return errors.New("empty pattern")
	}
	return module.CheckImportPath(prefix)
}

func TestCheckPattern(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"a/b/c", ""},
		{"a/b/c/...", ""},
		{"...", ""},
		{"", "empty"},
		{"a/.../b", "invalid"},
		{"a/b/", "malformed"},
		{"a/b/....", "invalid"},
		{"/...", "empty"},
		{"/a/b/...", "malformed"},
	} {
		got := checkPattern(tc.in)
		if got == nil {
			if tc.want != "" {
				t.Errorf("%q: unexpected success", tc.in)
			}
		} else if tc.want == "" {
			t.Errorf("%q: unexpected error %q", tc.in, got)
		} else if !strings.Contains(got.Error(), tc.want) {
			t.Errorf("%q: error %q does not contain %q", tc.in, got, tc.want)
		}
	}
}

// matchPattern reports whether pattern matches target.
// It assumes pattern has been validated with checkPattern.
// matchPattern will treat a test package path, with the suffix ".test",
// as equivalent to the non-test package.
func matchPattern(pattern, target string) bool {
	prefix, found := strings.CutSuffix(pattern, "...")
	if !found {
		return target == pattern || strings.TrimSuffix(target, ".test") == pattern
	}
	if prefix == "" {
		// The pattern "..." matches everything.
		return true
	}

	matches := func(targ string) bool {
		return targ == prefix[:len(prefix)-1] || strings.HasPrefix(targ, prefix)
	}

	return matches(target) || matches(strings.TrimSuffix(target, ".test"))
}

func TestMatchPattern(t *testing.T) {
	target := "some/import/path.test"
	for _, tc := range []struct {
		pattern string
		want    bool
	}{
		{"some/import/path", true},
		{"some/import/...", true},
		{"some/...", true},
		{"...", true},
		{"some/other/...", false},
	} {
		if err := checkPattern(tc.pattern); err != nil {
			t.Fatal(err)
		}
		got := matchPattern(tc.pattern, target)
		if got != tc.want {
			t.Errorf("%q: got %t, want %t", tc.pattern, got, tc.want)
		}
	}
}

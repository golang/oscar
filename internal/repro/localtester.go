// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package repro

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"go/format"
	"go/scanner"
	"go/token"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/repo"
)

//go:embed runtester.go
var runtester []byte

// LocalTester is an implementation of [CaseTester] that runs
// commands on the local system. It assumes git and Go.
// This is only for testing and should not be used in production.
// It is exported so that it can be used by tests of other packages.
type LocalTester struct {
	lg      *slog.Logger
	tmpDir  string
	version int // current Go version
	goRepo  *repo.Repo
}

// Verify that [*LocalTester] implements [CaseTester].
var _ CaseTester = &LocalTester{}

// NewLocalTester returns a new [LocalTester].
func NewLocalTester(ctx context.Context, lg *slog.Logger) (*LocalTester, error) {
	tmpDir, err := os.MkdirTemp("", "gaby-local-tester")
	if err != nil {
		return nil, err
	}

	goRepo, err := repo.Clone(ctx, lg, "https://go.googlesource.com/go")
	if err != nil {
		return nil, err
	}
	goDir := goRepo.Dir()

	versionSource, err := os.ReadFile(filepath.Join(goDir, "src/internal/goversion/goversion.go"))
	if err != nil {
		return nil, err
	}
	re := regexp.MustCompile(`(?m)^const Version = (\d+)`)
	match := re.FindSubmatch(versionSource)
	if len(match) == 0 {
		return nil, errors.New("internal/goversion/goversion.go does not contain 'const Version = ...'")
	}
	version, err := strconv.Atoi(string(match[1]))
	if err != nil {
		return nil, fmt.Errorf("failed to parse Go version: %v", err)
	}

	if err := os.WriteFile(filepath.Join(tmpDir, "runtester.go"), runtester, 0o444); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "go", "build", "runtester.go")
	cmd.Dir = tmpDir
	if err := run(lg, cmd); err != nil {
		return nil, err
	}

	testdir := filepath.Join(tmpDir, "testdir")
	if err := os.Mkdir(testdir, 0o755); err != nil {
		return nil, err
	}

	cmd = exec.CommandContext(ctx, "go", "mod", "init", "localtester")
	cmd.Dir = testdir
	if err := run(lg, cmd); err != nil {
		return nil, err
	}

	// Default to go1.20 for now. The reproduction case may
	// require a sufficiently new Go version, but we don't have
	// a way to detect that yet.
	cmd = exec.CommandContext(ctx, "go", "mod", "edit", "-go=1.20")
	cmd.Dir = testdir
	if err := run(lg, cmd); err != nil {
		return nil, err
	}

	lt := &LocalTester{
		lg:      lg,
		tmpDir:  tmpDir,
		version: version,
		goRepo:  goRepo,
	}
	return lt, nil
}

// Destroy destroys a [LocalTester], removing the temporary directory.
func (lt *LocalTester) Destroy() {
	lt.goRepo.Release()
	if err := os.RemoveAll(lt.tmpDir); err != nil {
		lt.lg.Error("failed to remove temporary directory", "dir", lt.tmpDir, "err", err)
	}
}

// Clean adds a package declaration and imports to try to
// make a test case runnable. This implements [CaseTester.Clean].
func (lt *LocalTester) Clean(ctx context.Context, bodyStr string) (string, error) {
	body := []byte(bodyStr)
	body, err := lt.addPackage(body)
	if err != nil {
		return "", err
	}
	body, err = lt.addImports(ctx, body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// addPackage adds a package declaration to the start of body if needed.
func (lt *LocalTester) addPackage(body []byte) ([]byte, error) {
	var s scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("t.go", fset.Base(), len(body))

	s.Init(file, body, nil, 0)

	_, tok, _ := s.Scan()
	if tok == token.PACKAGE {
		_, tok, lit := s.Scan()
		if tok != token.IDENT {
			return nil, fmt.Errorf("can't parse: package followed by %s", tok)
		}
		body = lt.addPrefix(body, lit)
		return body, nil
	}

	// No package declaration. Look for the function names.

	firstFuncName := ""
	firstFuncNameExported := false
	sawMain := false
	for {
		pos, tok, _ := s.Scan()
		if tok == token.ILLEGAL {
			return nil, fmt.Errorf("can't parse: invalid token at %s", fset.Position(pos))
		}
		if tok == token.EOF {
			break
		}
		if tok != token.FUNC {
			continue
		}

		_, tok, lit := s.Scan()
		if tok != token.IDENT {
			return nil, fmt.Errorf("can't parse: first func followed by %s", tok)
		}
		if lit == "main" {
			sawMain = true
		}
		firstRune, _ := utf8.DecodeRuneInString(lit)
		exported := unicode.IsUpper(firstRune)
		if firstFuncName == "" || (exported && !firstFuncNameExported) {
			firstFuncName = lit
			firstFuncNameExported = exported
		}
	}

	var packageName string
	if firstFuncName == "" {
		// No functions found; add a package declaration
		// and hope for the best.
		packageName = "p"
	} else if sawMain {
		packageName = "main"
	} else if strings.HasPrefix(firstFuncName, "Test") {
		packageName = "p_test"
	} else {
		packageName = "p"
	}

	body = append([]byte("package "+packageName+"\n"), body...)
	body = lt.addPrefix(body, packageName)
	return body, nil
}

// addPrefix adds a prefix to the body indicating how to run the code,
// based on the package name.
func (lt *LocalTester) addPrefix(body []byte, packageName string) []byte {
	var cmd string
	if packageName == "main" {
		cmd = "go run"
	} else if strings.HasSuffix(packageName, "_test") {
		cmd = "go test"
	} else {
		cmd = "go build"
	}
	return append([]byte("//"+cmd+"\n"), body...)
}

// addImports invokes goimports, if available, to add import statements.
// If goimports is not available, addImports just formats body.
func (lt *LocalTester) addImports(ctx context.Context, body []byte) ([]byte, error) {
	goimports, err := exec.LookPath("goimports")
	if err != nil {
		return lt.format(body)
	}

	cmd := exec.CommandContext(ctx, goimports)
	cmd.Stdin = bytes.NewReader(body)
	out, err := cmd.Output()
	if err != nil {
		lt.lg.Debug("goimports failed", "err", err, "out", string(out))
		return lt.format(body)
	}

	return out, nil
}

// format formats body.
func (lt *LocalTester) format(body []byte) ([]byte, error) {
	formatted, err := format.Source(body)
	if err != nil {
		return nil, fmt.Errorf("can't format: %v", err)
	}
	return formatted, nil
}

// CleanVersions tries to make the versions guessed by the LLM valid.
// It implements [CaseTester.CleanVersions].
func (lt *LocalTester) CleanVersions(ctx context.Context, pass, fail string) (string, string) {
	passOrig, failOrig := pass, fail

	fixup := func(v string) string {
		// Trim anything after a dash.
		// The LLM likes to guess go1.NN-RRRRRRR.
		if i := strings.Index(v, "-"); i >= 0 {
			v = v[:i]
		}

		// Turn a version like 1.23 into go1.23.
		if strings.HasPrefix(v, "1.") {
			v = "go" + v
		}

		// Turn a version like 1.23.2 into release-branch.go1.23.
		major := strings.TrimPrefix(v, "go1.")
		if i := strings.Index(major, "."); i >= 0 {
			major = major[:i]
		}
		if majorNum, err := strconv.Atoi(major); err == nil {
			if majorNum < lt.version {
				v = "release-branch.go1." + major
			} else if majorNum == lt.version {
				v = "master"
			} else {
				// If the current version is 1.24,
				// and the LLM guesses 1.25,
				// that is a pure hallucination.
				v = "unknown"
			}
		}

		return v
	}

	pass = fixup(pass)
	fail = fixup(fail)

	if fail == "" || fail == "unknown" {
		// We don't know the fail version, guess tip.
		fail = "master"
	}

	if pass == "" || pass == "unknown" {
		// We don't know the pass version, guess the last release.
		pass = "release-branch.go1." + strconv.Itoa(lt.version-1)
	}

	if pass != passOrig || fail != failOrig {
		lt.lg.Debug("cleaned versions", "passOld", passOrig, "passNew", pass, "failOld", failOrig, "failNew", fail)
	}

	return pass, fail
}

// Try runs a test case at the suggested version.
// It implements [CaseTester.Try].
func (lt *LocalTester) Try(ctx context.Context, body, version string) (bool, error) {
	if err := lt.checkout(ctx, version); err != nil {
		return false, err
	}

	bodyFile := filepath.Join(lt.tmpDir, "testdir", "body.go")
	if err := os.WriteFile(bodyFile, []byte(body), 0o666); err != nil {
		return false, err
	}

	cmd := exec.CommandContext(ctx, filepath.Join(lt.tmpDir, "runtester"), bodyFile)
	cmd.Dir = lt.goRepo.Dir()
	if err := run(lt.lg, cmd); err != nil {
		return false, nil
	}
	return true, nil
}

// checkout checks out a version of the Go repo.
func (lt *LocalTester) checkout(ctx context.Context, version string) error {
	return lt.goRepo.Checkout(ctx, lt.lg, version)
}

// Bisect runs a bisection of a test case.
// This implements [CaseTester.Bisect].
func (lt *LocalTester) Bisect(ctx context.Context, issue *github.Issue, body, pass, fail string) (string, error) {
	runtester := filepath.Join(lt.tmpDir, "runtester")

	bodyFile := filepath.Join(lt.tmpDir, "testdir", "body.go")
	if err := os.WriteFile(bodyFile, []byte(body), 0o666); err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, "git", "bisect", "start", fail, pass)
	cmd.Dir = lt.goRepo.Dir()
	if err := run(lt.lg, cmd); err != nil {
		return "", err
	}

	cmd = exec.CommandContext(ctx, "git", "bisect", "run", runtester, bodyFile)
	cmd.Dir = lt.goRepo.Dir()
	if err := run(lt.lg, cmd); err != nil {
		return "", err
	}

	cmd = exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	cmd.Dir = lt.goRepo.Dir()
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s failed: %v", cmd, err)
	}

	outStr := strings.TrimSpace(string(out))

	// TODO: Somehow mark this as the failing commit.
	lt.lg.Debug("found failing commit", "rev", outStr)

	return outStr, nil
}

// run runs an [exec.Command].
func run(lg *slog.Logger, cmd *exec.Cmd) error {
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			lg.Error("command failed", "cmd", cmd.String(), "err", err, "stdout", out, "stderr", ee.Stderr)
		} else {
			lg.Error("command failed", "cmd", cmd.String(), "err", err, "stdout", out)
		}
		return fmt.Errorf("%s failed: %v", cmd, err)
	}
	lg.Info("command succeeded", "cmd", cmd.String(), "stdout", out)
	return nil
}

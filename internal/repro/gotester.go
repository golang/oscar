// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package repro

import (
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

	"golang.org/x/oscar/internal/repo"
)

const goGitRepo = "https://go.googlesource.com/go"

//go:embed runtester.go
var runtester []byte

// GoTester implements [CaseTester] except for the Bisect method,
// for test cases that are written in Go.
type GoTester struct {
	lg       *slog.Logger
	executor Executor
	tmpDir   string
	version  int // current Go version
	goRepo   *repo.Repo
}

// An Executor is used to execute a command.
type Executor interface {
	// Execute runs the cmd, with args, in dir.
	// It returns the standard output,
	// and an error that may be [*os/exec.ExitError].
	Execute(ctx context.Context, lg *slog.Logger, dir string, cmd string, args ...string) ([]byte, error)
}

// NewGoTester returns a new [GoTester].
func NewGoTester(ctx context.Context, lg *slog.Logger, executor Executor) (*GoTester, error) {
	tmpDir, err := os.MkdirTemp("", "gaby-go-tester")
	if err != nil {
		return nil, err
	}

	goRepo, err := repo.Clone(ctx, lg, goGitRepo)
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

	if _, err := executor.Execute(ctx, lg, tmpDir, "go", "build", "runtester.go"); err != nil {
		return nil, err
	}

	testdir := filepath.Join(tmpDir, "testdir")
	if err := os.Mkdir(testdir, 0o755); err != nil {
		return nil, err
	}

	if _, err := executor.Execute(ctx, lg, testdir, "go", "mod", "init", "gotester"); err != nil {
		return nil, err
	}

	// Default to go1.20 for now. The reproduction case may
	// require a sufficiently new Go version, but we don't have
	// a way to detect that yet.
	if _, err := executor.Execute(ctx, lg, testdir, "go", "mod", "edit", "-go=1.20"); err != nil {
		return nil, err
	}

	gt := &GoTester{
		lg:       lg,
		executor: executor,
		tmpDir:   tmpDir,
		version:  version,
		goRepo:   goRepo,
	}
	return gt, nil
}

// Destroy destroys a [GoTester], removing the temporary directory.
func (gt *GoTester) Destroy() {
	gt.goRepo.Release()
	if err := os.RemoveAll(gt.tmpDir); err != nil {
		gt.lg.Error("failed to remove temporary directory", "dir", gt.tmpDir, "err", err)
	}
}

// testDir returns the path to the test directory,
// which can be used to run tests.
func (gt *GoTester) testDir() string {
	return filepath.Join(gt.tmpDir, "testdir")
}

// repoDir returns the path to the Go repository.
func (gt *GoTester) repoDir() string {
	return gt.goRepo.Dir()
}

// runTester returns the path to the runtester executable.
func (gt *GoTester) runTester() string {
	return filepath.Join(gt.tmpDir, "runtester")
}

// Clean adds a package declaration and imports to try to
// make a test case runnable. This implements [CaseTester.Clean].
func (gt *GoTester) Clean(ctx context.Context, bodyStr string) (string, error) {
	body := []byte(bodyStr)
	body, err := gt.addPackage(body)
	if err != nil {
		return "", err
	}
	body, err = gt.addImports(ctx, body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// addPackage adds a package declaration to the start of body if needed.
func (gt *GoTester) addPackage(body []byte) ([]byte, error) {
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
		body = gt.addPrefix(body, lit)
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
	body = gt.addPrefix(body, packageName)
	return body, nil
}

// addPrefix adds a prefix to the body indicating how to run the code,
// based on the package name.
func (gt *GoTester) addPrefix(body []byte, packageName string) []byte {
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
func (gt *GoTester) addImports(ctx context.Context, body []byte) ([]byte, error) {
	goimports, err := exec.LookPath("goimports")
	if err != nil {
		return gt.format(body)
	}

	goFile := filepath.Join(gt.tmpDir, "testdir", "imports.go")
	if err := os.WriteFile(goFile, body, 0o666); err != nil {
		return nil, err
	}
	defer os.Remove(goFile)

	_, err = gt.executor.Execute(ctx, gt.lg, "", goimports, goFile)
	if err != nil {
		return nil, err
	}

	body, err = os.ReadFile(goFile)
	if err != nil {
		return nil, err
	}

	return body, nil
}

// format formats body.
func (gt *GoTester) format(body []byte) ([]byte, error) {
	formatted, err := format.Source(body)
	if err != nil {
		return nil, fmt.Errorf("can't format: %v", err)
	}
	return formatted, nil
}

// CleanVersions tries to make the versions guessed by the LLM valid.
// It implements [CaseTester.CleanVersions].
func (gt *GoTester) CleanVersions(ctx context.Context, pass, fail string) (string, string) {
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
			if majorNum < gt.version {
				v = "release-branch.go1." + major
			} else if majorNum == gt.version {
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
		pass = "release-branch.go1." + strconv.Itoa(gt.version-1)
	}

	if pass != passOrig || fail != failOrig {
		gt.lg.Debug("cleaned versions", "passOld", passOrig, "passNew", pass, "failOld", failOrig, "failNew", fail)
	}

	return pass, fail
}

// Try runs a test case at the suggested version.
// It implements [CaseTester.Try].
func (gt *GoTester) Try(ctx context.Context, body, version string) (bool, error) {
	if err := gt.goRepo.Checkout(ctx, gt.lg, version); err != nil {
		return false, err
	}

	bodyFile := filepath.Join(gt.tmpDir, "testdir", "body.go")
	if err := os.WriteFile(bodyFile, []byte(body), 0o666); err != nil {
		return false, err
	}

	_, err := gt.executor.Execute(ctx, gt.lg, gt.goRepo.Dir(), gt.runTester(), bodyFile)
	if err != nil {
		return false, nil
	}

	return true, nil
}

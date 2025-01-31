// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package repro

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/oscar/internal/github"
)

// LocalTester is an implementation of [CaseTester] that runs
// commands on the local system. It assumes git and Go.
// This is only for testing and should not be used in production.
// It is exported so that it can be used by tests of other packages.
type LocalTester struct {
	lg       *slog.Logger
	goTester *GoTester
}

// Verify that [*LocalTester] implements [CaseTester].
var _ CaseTester = &LocalTester{}

// NewLocalTester returns a new [LocalTester].
func NewLocalTester(ctx context.Context, lg *slog.Logger) (*LocalTester, error) {
	gt, err := NewGoTester(ctx, lg, localExecutor{})
	if err != nil {
		return nil, err
	}

	lt := &LocalTester{
		lg:       lg,
		goTester: gt,
	}
	return lt, nil
}

// Destroy destroys a [LocalTester], removing the temporary directory.
func (lt *LocalTester) Destroy() {
	lt.goTester.Destroy()
}

// Clean adds a package declaration and imports to try to
// make a test case runnable. This implements [CaseTester.Clean].
func (lt *LocalTester) Clean(ctx context.Context, bodyStr string) (string, error) {
	return lt.goTester.Clean(ctx, bodyStr)
}

// CleanVersions tries to make the versions guessed by the LLM valid.
// It implements [CaseTester.CleanVersions].
func (lt *LocalTester) CleanVersions(ctx context.Context, pass, fail string) (string, string) {
	return lt.goTester.CleanVersions(ctx, pass, fail)
}

// Try runs a test case at the suggested version.
// It implements [CaseTester.Try].
func (lt *LocalTester) Try(ctx context.Context, body, version string) (bool, error) {
	return lt.goTester.Try(ctx, body, version)
}

// Bisect runs a bisection of a test case.
// This implements [CaseTester.Bisect].
func (lt *LocalTester) Bisect(ctx context.Context, issue *github.Issue, body, pass, fail string) (string, error) {
	runtester := lt.goTester.runTester()

	bodyFile := filepath.Join(lt.goTester.testDir(), "body.go")
	if err := os.WriteFile(bodyFile, []byte(body), 0o666); err != nil {
		return "", err
	}

	repoDir := lt.goTester.repoDir()
	var le localExecutor
	_, err := le.Execute(ctx, lt.lg, repoDir, "git", "bisect", "start", fail, pass)
	if err != nil {
		return "", err
	}

	_, err = le.Execute(ctx, lt.lg, repoDir, "git", "bisect", "run", runtester, bodyFile)
	if err != nil {
		return "", err
	}

	out, err := le.Execute(ctx, lt.lg, repoDir, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}

	outStr := strings.TrimSpace(string(out))

	// TODO: Somehow mark this as the failing commit.
	lt.lg.Debug("found failing commit", "rev", outStr)

	return outStr, nil
}

// localExecutor implements [Executor] by running programs locally.
type localExecutor struct{}

// Execute implements [Executor.Execute].
func (localExecutor) Execute(ctx context.Context, lg *slog.Logger, dir string, command string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			lg.Error("command failed", "cmd", cmd.String(), "err", err, "stdout", out, "stderr", ee.Stderr)
		} else {
			lg.Error("command failed", "cmd", cmd.String(), "err", err, "stdout", out)
		}
		return nil, fmt.Errorf("%s failed: %v", cmd, err)
	}
	lg.Info("command succeeded", "cmd", cmd.String(), "stdout", out)
	return out, nil
}

// LookPath implements [Executor.LookPath].
func (localExecutor) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

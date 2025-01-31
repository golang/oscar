// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testutil

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
)

// StubExecutor is a stub Executor for testing.
// This implements [repo.Executor] and [repro.Executor].
type StubExecutor struct {
	stubs []*Stub
	paths map[string]string
}

// Stub is a single stub execution.
// If asked to run cmd/args, instead call fn with the directory
// and return what fn returns.
type Stub struct {
	cmd  string
	args []string
	fn   func(dir string) ([]byte, error)
}

// Add adds a new stub execution.
// An execution of cmd/args will call the function,
// and return what it returns.
func (se *StubExecutor) Add(cmd string, args []string, fn func(dir string) ([]byte, error)) {
	// Replace an existing stub if there is one.
	for _, st := range se.stubs {
		if st.cmd == cmd && slices.Equal(st.args, args) {
			st.fn = fn
			return
		}
	}

	st := &Stub{
		cmd:  cmd,
		args: args,
		fn:   fn,
	}
	se.stubs = append(se.stubs, st)
}

// Execute implements [repo.Executor.Execute].
func (se *StubExecutor) Execute(ctx context.Context, lg *slog.Logger, dir, cmd string, args ...string) ([]byte, error) {
	for _, st := range se.stubs {
		if st.cmd == cmd && slices.Equal(st.args, args) {
			return st.fn(dir)
		}
	}
	return nil, fmt.Errorf("no stub for %s %q", cmd, args)
}

// AddPath adds a path to be returned by LookPath.
func (se *StubExecutor) AddPath(file, path string) {
	if se.paths == nil {
		se.paths = make(map[string]string)
	}
	se.paths[file] = path
}

// LookPath implements [repro.Executor.LookPath].
func (se *StubExecutor) LookPath(file string) (string, error) {
	if path, ok := se.paths[file]; ok {
		return path, nil
	}
	return "", fmt.Errorf("no stub path for %s", file)
}

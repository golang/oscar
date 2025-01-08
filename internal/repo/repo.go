// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package repo has functions to manage a checked out copy of
// a git repository.
package repo

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

var (
	// reposLock is a lock for the repos variable.
	reposLock sync.Mutex

	// repos holds released repos as a map from a url.
	// It may only be accessed while reposLock is held.
	repos = make(map[string][]*Repo)
)

// Repo is a clone of a git repo.
type Repo struct {
	url    string // repo URL
	dir    string // local temporary directory
	subdir string // subdirectory holding git repo
}

// Dir returns the local directory holding the Repo.
func (repo *Repo) Dir() string {
	return filepath.Join(repo.dir, repo.subdir)
}

// Release releases a Repo when it is no longer needed.
func (repo *Repo) Release() {
	reposLock.Lock()
	defer reposLock.Unlock()
	repos[repo.url] = append(repos[repo.url], repo)
}

// acquire returns an unused Repo for a git repo, if there is one.
// If there isn't one, it returns nil.
func acquire(url string) *Repo {
	reposLock.Lock()
	defer reposLock.Unlock()
	s := repos[url]
	ln := len(s)
	if ln == 0 {
		return nil
	}
	ret := s[ln-1]
	repos[url] = s[:ln-1]
	return ret
}

// Clone returns a new clone of a git repo, or reuses an existing one.
func Clone(ctx context.Context, lg *slog.Logger, url string) (r *Repo, err error) {
	if r := acquire(url); r != nil {
		return r, nil
	}

	dir, err := os.MkdirTemp("", "gaby-git-repo")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
		}
	}()

	r = &Repo{
		url: url,
		dir: dir,
	}

	lg.Debug("cloning git repo", "repo", url)

	cmd := exec.CommandContext(ctx, "git", "clone", url)
	cmd.Dir = dir
	if err := run(lg, cmd); err != nil {
		return nil, err
	}

	// We expect the "git clone" to create a single directory inside dir.
	subdirs, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	if len(subdirs) != 1 {
		return nil, fmt.Errorf("repo.Clone: ReadDir(%q) = %v, expected a single subdirectory", dir, subdirs)
	}
	r.subdir = subdirs[0].Name()

	return r, nil
}

// Checkout checks out a specific version of a git repo.
func (repo *Repo) Checkout(ctx context.Context, lg *slog.Logger, version string) error {
	cmd := exec.CommandContext(ctx, "git", "checkout", version)
	cmd.Dir = filepath.Join(repo.dir, repo.subdir)
	return run(lg, cmd)
}

// FreeAll frees all cached repositories and removes the directories.
func FreeAll() {
	reposLock.Lock()

	var dirs []string
	for _, s := range repos {
		for _, r := range s {
			dirs = append(dirs, r.dir)
		}
	}
	clear(repos)

	reposLock.Unlock()

	for _, dir := range dirs {
		os.RemoveAll(dir)
	}
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
	lg.Debug("command succeeded", "cmd", cmd.String(), "dir", cmd.Dir, "stdout", out)
	return nil
}

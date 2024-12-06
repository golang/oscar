// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package bisect is used for bisecting a target repository
// with the goal of finding a commit introducing a regression.
package bisect

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/queue"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

const (
	taskKind       = "bisection.Task"
	taskUpdateKind = "bisection.TaskUpdate" // used for storing task updates in a timed db
)

// This package stores the following key schemas in the database:
//
//	["bisection.Task", ID] => JSON of Task structure
//	["bisection.TaskUpdateByTime", DBTime, ID] => []
//
// Bisecting a repository for a change regression can take considerable
// time. This has an effect on how the bisection is run in gaby. If
// bisection is being run as part of a batch job, other jobs will be
// blocked by the bisection. Spawning a bisection in a goroutine
// or a process will in principle not work on Cloud Run, which can
// move or kill a gaby instance if there are no requests served [1],
// even if several bisections are being ran in the background.
//
// This package addresses this problem by asynchronous bisection.
// [Client.BisectAsync] spawns a bisection [Task] by sending it to
// a [queue.Queue], which in practice will be a Cloud Tasks [2]
// queue. The latter will then send a request to gaby, which in
// turn will call [Client.Bisect]. The results and partial progress
// of bisection are saved to the provided database.
//
// [1] https://cloud.google.com/run/docs/about-instance-autoscaling
// [2] https://cloud.google.com/tasks/docs

// o is short for ordered.Encode.
func o(list ...any) []byte { return ordered.Encode(list...) }

// A Client is responsible for dispatching
// and executing bisection tasks.
type Client struct {
	slog  *slog.Logger
	db    storage.DB
	queue queue.Queue

	// testing is used to divert
	// bisection to artificial
	// results, for testing purposes.
	testing bool
}

// New returns a new client for bisection.
// The client uses the given logger, database, and queue.
func New(lg *slog.Logger, db storage.DB, q queue.Queue) *Client {
	return &Client{
		slog:  lg,
		db:    db,
		queue: q,
	}
}

// BisectAsync creates and spawns a bisection task for trigger
// if the latter encodes a request for bisection. Otherwise, it
// does nothing and returns nil.
//
// BisectAsync creates a [Task] and saves it to the database,
// and then triggers an asynchronous execution of [Client.Bisect]
// through [Client] queue.
//
// TODO: generalize trigger beyond GitHub issue comment.
func (c *Client) BisectAsync(ctx context.Context, trigger *github.IssueComment) error {
	if trigger.Project() != "golang/go" {
		return fmt.Errorf("bisect.Add: only golang/go repo currently supported, got '%s'", trigger.Project())
	}

	now := time.Now()
	t := &Task{
		Trigger:    trigger.URL,
		Issue:      trigger.IssueURL,
		Repository: "https://go.googlesource.com/go",
		Bad:        "master",
		Good:       "go1.22.0",
		Regression: regression(trigger.Body),
		Created:    now,
		Updated:    now,
	}
	t.ID = newTaskID(t)

	skey := string(o(taskKind, t.ID))
	// Lock the task for sanity.
	// This also helps with testing
	// when enqueued bisection starts
	// before BisectAsync saves the
	// task to the database.
	c.db.Lock(skey)
	defer c.db.Unlock(skey)

	ok, err := c.queue.Enqueue(ctx, t, &queue.Options{})
	c.slog.Info("bisect.BisectAsync: enqueueing bisection task", "id", t.ID, "issue", t.Issue, "enqueued", ok)
	if ok {
		// Save the task only if it is enqueued.
		t.Status = StatusQueued
		c.save(t)
	}
	return err
}

// regression extracts a bisection
// test code from body.
func regression(body string) string {
	// For now, assume the body is
	// the regression code.
	return body
}

// newTaskID creates a unique hex ID for t based
// on the repository, issue, trigger, command, and
// bisect commit information.
func newTaskID(t *Task) string {
	hasher := sha256.New()
	io.WriteString(hasher, t.Trigger)
	io.WriteString(hasher, t.Repository)
	io.WriteString(hasher, t.Issue)
	io.WriteString(hasher, t.Good)
	io.WriteString(hasher, t.Bad)
	io.WriteString(hasher, t.Regression)
	return hex.EncodeToString(hasher.Sum(nil))
}

// task returns [Task] with ID equal to id from the
// database, if such task exists. It returns nil otherwise.
func (c *Client) task(id string) (*Task, error) {
	key := o(taskKind, id)
	tj, ok := c.db.Get(key)
	if !ok {
		return nil, nil
	}
	var t Task
	if err := json.Unmarshal(tj, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// save the task to the database.
func (c *Client) save(t *Task) {
	b := c.db.Batch()
	key := o(taskKind, t.ID)
	b.Set(key, storage.JSON(t))
	timed.Set(c.db, b, taskUpdateKind, o(t.ID), nil)
	b.Apply()
	c.db.Flush()
}

// Bisect performs bisection on task with task id.
func (c *Client) Bisect(id string) error {
	skey := string(o(taskKind, id))
	// Lock the task just in case, so that
	// no one else is bisecting it concurrently.
	c.db.Lock(skey)
	defer c.db.Unlock(skey)

	t, err := c.task(id)
	if err != nil || t == nil {
		return fmt.Errorf("bisect.Bisect: task could not be found id=%s err=%v", id, err)
	}

	// TODO: handle retries.
	// If a task with the t.ID already exists and it has been more
	// than cloud-task-deadline minutes since the task has been updated,
	// assume the task was killed and restart the task from where it
	// stopped the last time it was updated?

	dir, err := os.MkdirTemp("", "bisect")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	c.slog.Info("bisect.Bisect started", "id", id)
	t.Status = StatusStarted
	c.save(t)
	err = c.bisect(dir, t)
	if err != nil {
		t.Status = StatusFailed
		t.Error = err.Error()
	} else {
		t.Status = StatusSucceeded
		t.Result = t.Output
	}
	c.save(t)
	c.slog.Info("bisect.Bisect finished", "id", id, "err", t.Error, "log", t.Result)
	return err
}

// bisectScript is a template that compiles a Go repo
// and runs a task regression in a gvisor sandbox against
// the repo. It should be instantiated with a unique
// sandbox identifier.
const bisectScript = `#!/bin/bash -eu

# This script expects that it is in the target go repository
# that sits at the top of bundle/rootfs/.

# Build go.
git clean -df
cd src
./make.bash || exit 125

# Go back to bundle and run the sandbox. The sandbox reads
# data from the bundle/rootfs but it cannot make changes
# to the directory visible outside of the sandbox.
cd ../../../
/usr/local/bin/runsc --ignore-cgroups --network=none --platform=systrap run sandbox%s
`

// bisect performs a bisection of t inside dir.
// It assumes that gvisor's bisect_config.json and
// its entry point bisect_runner are in the current
// working directory.
func (c *Client) bisect(dir string, t *Task) error {
	if c.testing {
		t.Output = "000000000001 is the first bad commit"
		c.save(t)
		// TODO: can we test this better?
		return nil
	}

	// bundle is the place from which the
	// sandbox must be created.
	bundle := filepath.Join(dir, "bundle")
	if err := os.Mkdir(bundle, 0o777); err != nil {
		return err
	}

	// bisect_config.json must be present in
	// the bundle as config.json.
	if err := cp("bisect_config.json", filepath.Join(bundle, "config.json")); err != nil {
		return err
	}

	// rootfs is the directory in bundle that
	// will be the root of the sandbox execution.
	rootfs := filepath.Join(bundle, "rootfs")
	if err := os.Mkdir(rootfs, 0o777); err != nil {
		return err
	}

	// Save the regression as the regression
	// test in rootfs.
	if err := os.WriteFile(filepath.Join(rootfs, "regression_test.go"), []byte(t.Regression), 0o666); err != nil {
		return err
	}

	// Generate and save the bisection script in rootfs.
	bisectCode := fmt.Sprintf(bisectScript, t.ID)
	if err := os.WriteFile(filepath.Join(rootfs, "bisect.sh"), []byte(bisectCode), 0o750); err != nil {
		return err
	}

	// Copy bisect_runner to rootfs as the entry
	// point to the sandbox.
	if err := cp("bisect_runner", filepath.Join(rootfs, "bisect_runner")); err != nil {
		return err
	}
	c.slog.Info("bisect.Bisect: created and copied all the scripts", "id", t.ID, "bundle", bundle, "rootfs", rootfs)

	// Clone the go repo to rootfs as go-bisect.
	gobisect := filepath.Join(rootfs, "go-bisect")
	if err := os.Mkdir(gobisect, 0o777); err != nil {
		return err
	}
	gitclone := exec.Command("git", "clone", "https://go.googlesource.com/go", gobisect)
	if err := run(gitclone, t, c); err != nil {
		return err
	}
	c.slog.Info("bisect.Bisect: cloned the go repo", "id", t.ID, "dir", gobisect)

	// Initialize git bisect.
	bisectstart := exec.Command("git", "bisect", "start", t.Bad, t.Good)
	bisectstart.Dir = gobisect
	if err := run(bisectstart, t, c); err != nil {
		return err
	}
	c.slog.Info("bisect.Bisect: initialized git bisect", "id", t.ID)

	// Run git bisect.
	bisectrun := exec.Command("git", "bisect", "run", "../bisect.sh")
	bisectrun.Dir = gobisect
	if err := run(bisectrun, t, c); err != nil {
		return err
	}

	return nil
}

// run runs cmd while simultaneously
// listening and saving cmd's output
// to t.Output.
func run(cmd *exec.Cmd, t *Task, c *Client) error {
	// TODO: also read from stderr.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	// TODO: avoid using scanner and appending
	// strings for commands with large ouput
	// with long lines.
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return err
		}
		m := scanner.Text()
		t.Output += m + "\n"
		t.Updated = time.Now()
		c.save(t)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return cmd.Wait()
}

// cp copies the src file to dst
// location. The destination file
// is freshly created with the same
// permissions as the source file.
func cp(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	s, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, s, info.Mode())
}

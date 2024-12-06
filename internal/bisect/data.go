// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bisect

import (
	"encoding/json"
	"fmt"
	"iter"
	"time"

	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

// Status is the status of the bisection task.
type Status int

const (
	// Bisection task is ready to start.
	StatusReady Status = iota
	// Bisection task is enqueued.
	StatusQueued
	// Bisection task is in progress.
	StatusStarted
	// Bisection task failed.
	StatusFailed
	// Bisection task finished successfully.
	StatusSucceeded
)

// Task contains metadata and progress information
// on bisection run that is saved to the database
// and also used as a Cloud Task queue entry.
type Task struct {
	// ID is the unique identifier for the task.
	ID string
	// Trigger identifies what triggered the
	// bisection task. For instance, it can
	// be a GitHub comment requesting a bisection.
	Trigger string
	// Issue identifies the problem associated
	// with the bisection. For instance, Issue
	// can be a GitHub issue for which the
	// bisection is ran.
	Issue string
	// Repository is the repo on which the
	// bisection is performed.
	Repository string
	// Bad is the commit hash or tag
	// from which the bisection starts.
	Bad string
	// Good is the commit hash or tag
	// at which the bisection ends.
	Good string
	// Regression is Go code reproducing a
	// bug that needs to be bisected. Currently,
	// it is expected to be a Go test.
	Regression string
	// Output is the output of bisection. It
	// can contain progress and debug messages.
	Output string
	// Result encodes the bisection finding.
	Result string
	// Error is a message describing bisection
	// failure, if any.
	Error string
	// Status is the status of the bisection.
	Status Status
	// Updated is the last time the bisection
	// task data was updated. Together with
	// Status, Updated can be used to infer
	// when the task finished.
	Updated time.Time
	// Created is the time the bisection task
	// was created and queued for execution.
	Created time.Time
}

// Name of a task is its issue combined with ID.
func (t *Task) Name() string {
	return fmt.Sprintf("%s-%s", t.Issue, t.ID)
}

// Path is always "bisect". This is the gaby
// endpoint to which the task data will be sent.
func (t *Task) Path() string {
	return "bisect"
}

// Params encodes task ID.
func (t *Task) Params() string {
	return "id=" + t.ID
}

// BisectionTasks returns an iterator over the bisection tasks.
// The first iterator value is the task ID and the other value
// is the task itself.
func (c *Client) BisectionTasks() iter.Seq2[string, *Task] {
	return func(yield func(string, *Task) bool) {
		for key, fn := range c.db.Scan(o(taskKind), o(taskKind, ordered.Inf)) {
			var id string
			if err := ordered.Decode(key, nil, &id); err != nil {
				c.db.Panic("bisect client task decode", "key", storage.Fmt(key), "err", err)
			}
			var t Task
			if err := json.Unmarshal(fn(), &t); err != nil {
				c.db.Panic("bisect client task unmarshal", "key", storage.Fmt(key), "err", err)
			}
			if !yield(id, &t) {
				return
			}
		}
	}
}

// TaskWatcher returns a new [timed.Watcher] with the given name.
// It picks up where any previous Watcher of the same name left off.
func (c *Client) TaskWatcher(name string) *timed.Watcher[*TaskEvent] {
	return timed.NewWatcher(c.slog, c.db, name, taskUpdateKind, c.decodeTaskEvent)
}

// decodeTaskEvent decodes a taskUpdateKind [timed.Entry] into
// a task event.
func (c *Client) decodeTaskEvent(t *timed.Entry) *TaskEvent {
	te := TaskEvent{
		DBTime: t.ModTime,
	}
	if err := ordered.Decode(t.Key, &te.ID); err != nil {
		c.db.Panic("bisect task event decode", "key", storage.Fmt(t.Key), "err", err)
	}
	return &te
}

// A TaskEvent is a bisection [Task]
// event returned by bisection watchers.
type TaskEvent struct {
	DBTime timed.DBTime // when event was created
	ID     string       // ID of the bisection task
}

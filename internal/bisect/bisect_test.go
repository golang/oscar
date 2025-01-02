// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bisect

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"golang.org/x/oscar/internal/queue"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestNewTaskID(t *testing.T) {
	created := time.Date(2024, time.January, 0, 0, 0, 0, 0, time.UTC) // fixed date
	for _, test := range []struct {
		task Task
		want string
	}{
		{
			Task{Trigger: "t", Issue: "i", Repository: "r", Regression: "c", Good: "g", Bad: "b"},
			"182eae594755dfbfbdba6d5c312d3655fbcc9dd634c818ebaf2da1dd7b6bb808",
		},
		// Status, ID, Output, Created, and Updated are not important for ID computation.
		{
			Task{ID: "id", Trigger: "t", Issue: "i", Repository: "r", Regression: "c", Good: "g",
				Bad: "b", Output: "o", Updated: time.Now(), Status: StatusSucceeded, Created: created},
			"182eae594755dfbfbdba6d5c312d3655fbcc9dd634c818ebaf2da1dd7b6bb808",
		},
	} {
		got := newTaskID(&test.task)
		if got != test.want {
			t.Errorf("%v: got %s, want %s", test.task, got, test.want)
		}
	}
}

func TestBisectAsync(t *testing.T) {
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	ctx := context.Background()

	var c *Client
	// Process simulates what [Client.BisectAsync] will do in prod:
	// send a task to a Cloud Tasks queue, which will issue a [http.Request]
	// to gaby handle, which will then call [Client.Bisect] with the request.
	process := func(_ context.Context, t queue.Task) error {
		// Actual bisection handler will take an http
		// request and parse the id param similarly.
		url, err := url.Parse(t.Path() + "?" + t.Params())
		if err != nil {
			return err
		}
		return c.Bisect(url.Query().Get("id"))
	}
	q := queue.NewInMemory(ctx, 1, process)
	c = New(lg, db, q)
	c.testing = true

	req1 := &Request{
		Trigger: "https://api.github.com/repos/golang/go/issues/00001#issuecomment-000001",
		Issue:   "https://api.github.com/repos/golang/go/issues/00001",
		Body:    "body1",
	}
	req2 := &Request{
		Trigger: "https://api.github.com/repos/golang/go/issues/00002#issuecomment-000002",
		Issue:   "https://api.github.com/repos/golang/go/issues/00002",
		Body:    "body2",
	}
	check(c.BisectAsync(ctx, req1))
	check(c.BisectAsync(ctx, req2))

	q.Wait(ctx)
	check(errors.Join(q.Errors()...))

	w := c.TaskWatcher("test")
	var tasks []*Task
	for e := range w.Recent() {
		task, err := c.task(e.ID)
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, task)
	}

	if len(tasks) != 2 {
		t.Errorf("want 2 tasks; got %d", len(tasks))
	}
	wantResult := "000000000001 is the first bad commit"
	for _, task := range tasks {
		if task.Status != StatusSucceeded {
			t.Errorf("got %d status for %v; want %d", task.Status, task, StatusSucceeded)
		}
		if task.Error != "" {
			t.Errorf("got error %s for %v; want none", task.Error, task)
		}
		if task.Result != wantResult {
			t.Errorf("got %s status for %v; want %s", task.Result, task, wantResult)
		}
	}
}

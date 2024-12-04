// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package queue

import (
	"context"
	"fmt"
	"testing"
)

type testTask struct {
	name   string
	path   string
	params string
}

func (t *testTask) Name() string   { return t.name }
func (t *testTask) Path() string   { return t.path }
func (t *testTask) Params() string { return t.params }

func TestInMemoryQueue(t *testing.T) {
	t1 := &testTask{"name1", "path1", "params1"}
	t2 := &testTask{"name2", "path2", "params2"}
	t3 := &testTask{"", "path1", "params1"}

	process := func(_ context.Context, t Task) error {
		if t.Name() == "" {
			return fmt.Errorf("name not set for task with path %s", t.Path())
		}
		return nil
	}

	ctx := context.Background()
	q := NewInMemory(ctx, 2, process)
	q.Enqueue(ctx, t1, nil)
	q.Enqueue(ctx, t2, nil)
	q.Enqueue(ctx, t3, nil)
	q.Wait(ctx)

	errs := q.Errors()
	if len(errs) != 1 {
		t.Fatalf("want 1 error; got %d", len(errs))
	}

	want := "name not set for task with path path1"
	got := errs[0].Error()
	if want != got {
		t.Errorf("want '%s' as error message; got '%s'", want, got)
	}
}

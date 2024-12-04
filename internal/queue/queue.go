// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package queue provides queue interface and an in-memory
// implementation that can be used for asynchronous scheduling
// of fetch actions.
package queue

import (
	"context"
	"fmt"
	"time"
)

// A Task can produce information needed for Cloud Tasks.
type Task interface {
	Name() string   // Human-readable string for the task. Need not be unique.
	Path() string   // URL path
	Params() string // URL (escaped) query params
}

// A Queue provides an interface for asynchronous scheduling of tasks.
type Queue interface {
	// Enqueue enqueues a task request.
	// It reports whether a new task was actually added to the queue.
	Enqueue(context.Context, Task, *Options) (bool, error)
}

// Options is used to provide option arguments for a task queue.
type Options struct {
	// TaskNameSuffix is appended to the task name to force reprocessing of
	// tasks that would normally be de-duplicated.
	TaskNameSuffix string
}

// Metadata is data needed to create a Cloud Task Queue.
type Metadata struct {
	Project        string // Name of the gaby project.
	Location       string // Location of the queue (e.g., us-central1).
	QueueName      string // Unique ID of the queue.
	QueueURL       string // URL of the Cloud Run service.
	ServiceAccount string // Email of the service account associated with the project.
}

// InMemory is a Queue implementation that schedules in-process fetch
// operations. Unlike the GCP task queue, it will not automatically
// retry tasks on failure.
//
// This should only be used for local development and testing.
type InMemory struct {
	queue chan Task
	done  chan struct{}
	errs  []error
}

// NewInMemory creates a new InMemory that asynchronously schedules
// tasks and executes processFunc on them. It uses workerCount parallelism
// to accomplish this.
func NewInMemory(ctx context.Context, workerCount int, processFunc func(context.Context, Task) error) *InMemory {
	q := &InMemory{
		queue: make(chan Task, 1000),
		done:  make(chan struct{}),
	}
	sem := make(chan struct{}, workerCount)
	go func() {
		for v := range q.queue {
			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
			}

			// If a worker is available, spawn a task in a
			// goroutine and wait for it to finish.
			go func(t Task) {
				defer func() { <-sem }()

				fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
				defer cancel()

				if err := processFunc(fetchCtx, t); err != nil {
					q.errs = append(q.errs, err)
				}
			}(v)
		}
		for i := 0; i < cap(sem); i++ {
			select {
			case <-ctx.Done():
				// If context is cancelled here, there is no way for us to
				// do cleanup. We panic here since there is no other way to
				// report an error.
				panic(fmt.Sprintf("InMemory queue context done: %v", ctx.Err()))
			case sem <- struct{}{}:
			}
		}
		close(q.done)
	}()
	return q
}

// Enqueue pushes a scan task into the local queue to be processed
// asynchronously.
func (q *InMemory) Enqueue(ctx context.Context, task Task, _ *Options) (bool, error) {
	q.queue <- task
	return true, nil
}

// Wait waits for all queued requests to finish.
func (q *InMemory) Wait(ctx context.Context) {
	close(q.queue)
	<-q.done
}

func (q *InMemory) Errors() []error {
	return q.errs
}

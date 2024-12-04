// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package queue provides a GCP queue implementation that
// can be used for asynchronous scheduling of fetch actions.
package queue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	cloudtasks "cloud.google.com/go/cloudtasks/apiv2"
	taskspb "cloud.google.com/go/cloudtasks/apiv2/cloudtaskspb"
	gqueue "golang.org/x/oscar/internal/queue"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

// New creates a new Queue based on metadata m.
func New(ctx context.Context, m *gqueue.Metadata) (gqueue.Queue, error) {
	client, err := cloudtasks.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	g, err := newGCP(client, m)
	if err != nil {
		return nil, err
	}
	return g, nil
}

// GCP provides a Queue implementation backed by the Google Cloud Tasks API.
type GCP struct {
	client    *cloudtasks.Client
	queueName string // full GCP name of the queue
	queueURL  string // non-AppEngine URL to post tasks to
	// token holds information that lets the task queue construct an authorized request to the worker.
	// Since the worker sits behind the IAP, the queue needs an identity token that includes the
	// identity of a service account that has access, and the client ID for the IAP.
	// We use the service account of the current process.
	token *taskspb.HttpRequest_OidcToken
}

// newGCP returns a new Queue based on metadata m that can be used to
// enqueue tasks using the cloud tasks API. The given m.QueueName
// should be the name of the queue in the cloud tasks console.
func newGCP(client *cloudtasks.Client, m *gqueue.Metadata) (*GCP, error) {
	if m.QueueName == "" {
		return nil, errors.New("empty queue name")
	}
	if m.Project == "" {
		return nil, errors.New("empty project")
	}
	if m.QueueURL == "" {
		return nil, errors.New("empty queue URL")
	}
	if m.ServiceAccount == "" {
		return nil, errors.New("empty serviceAccount")
	}
	if m.Location == "" {
		return nil, errors.New("empty location")
	}
	return &GCP{
		client:    client,
		queueName: fmt.Sprintf("projects/%s/locations/%s/queues/%s", m.Project, m.Location, m.QueueName),
		queueURL:  m.QueueURL,
		token: &taskspb.HttpRequest_OidcToken{
			OidcToken: &taskspb.OidcToken{
				ServiceAccountEmail: m.ServiceAccount,
			},
		},
	}, nil
}

// Enqueue enqueues a task on GCP.
// It returns an error if there was an error hashing the task name, or
// an error pushing the task to GCP.
// If the task was a duplicate, it returns (false, nil).
func (q *GCP) Enqueue(ctx context.Context, task gqueue.Task, opts *gqueue.Options) (bool, error) {
	if opts == nil {
		opts = &gqueue.Options{}
	}
	// Cloud Tasks enforces an RPC timeout of at most 30s. I couldn't find this
	// in the documentation, but using a larger value, or no timeout, results in
	// an InvalidArgument error with the text "The deadline cannot be more than
	// 30s in the future."
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := q.newTaskRequest(task, opts)
	if err != nil {
		return false, fmt.Errorf("newTaskRequest: %v", err)
	}

	_, err = q.client.CreateTask(ctx, req)
	if err == nil {
		return true, nil
	}
	if status.Code(err) == codes.AlreadyExists {
		return false, nil
	}
	return false, fmt.Errorf("q.client.CreateTask(ctx, req): %v", err)
}

// maxCloudTasksTimeout is the maximum timeout for HTTP tasks.
// See https://cloud.google.com/tasks/docs/creating-http-target-tasks.
const maxCloudTasksTimeout = 30 * time.Minute

func (q *GCP) newTaskRequest(task gqueue.Task, opts *gqueue.Options) (*taskspb.CreateTaskRequest, error) {
	relativeURI := "/" + task.Path()
	if params := task.Params(); params != "" {
		relativeURI += "?" + params
	}

	taskID := newTaskID(task)
	taskpb := &taskspb.Task{
		Name:             fmt.Sprintf("%s/tasks/%s", q.queueName, taskID),
		DispatchDeadline: durationpb.New(maxCloudTasksTimeout),
		MessageType: &taskspb.Task_HttpRequest{
			HttpRequest: &taskspb.HttpRequest{
				HttpMethod:          taskspb.HttpMethod_POST,
				Url:                 q.queueURL + relativeURI,
				AuthorizationHeader: q.token,
			},
		},
	}
	req := &taskspb.CreateTaskRequest{
		Parent: q.queueName,
		Task:   taskpb,
	}
	// If suffix is non-empty, append it to the task name.
	// This lets us force reprocessing of tasks that would normally be de-duplicated.
	if opts.TaskNameSuffix != "" {
		req.Task.Name += "-" + opts.TaskNameSuffix
	}
	return req, nil
}

// newTaskID creates a task ID for the given task.
// Tasks with the same ID that are created within a few hours of each other will be de-duplicated.
// See https://cloud.google.com/tasks/docs/reference/rpc/google.cloud.tasks.v2#createtaskrequest
// under "Task De-duplication".
func newTaskID(task gqueue.Task) string {
	name := task.Name()
	// Hash the path, params, and body of the task.
	hasher := sha256.New()
	io.WriteString(hasher, task.Path())
	io.WriteString(hasher, task.Params())
	hash := hex.EncodeToString(hasher.Sum(nil))
	return escapeTaskID(fmt.Sprintf("%s-%s", name, hash[:8]))
}

// escapeTaskID escapes s so it contains only valid characters for a Cloud Tasks name.
// It tries to produce a readable result.
// Task IDs can contain only letters ([A-Za-z]), numbers ([0-9]), hyphens (-), or underscores (_).
func escapeTaskID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-':
			b.WriteRune(r)
		case r == '_':
			b.WriteString("__")
		case r == '/':
			b.WriteString("_-")
		case r == '@':
			b.WriteString("_")
		case r == '.':
			b.WriteString("_")
		default:
			fmt.Fprintf(&b, "_%04x", r)
		}
	}
	return b.String()
}

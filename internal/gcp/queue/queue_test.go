// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package queue

import (
	"testing"

	taskspb "cloud.google.com/go/cloudtasks/apiv2/cloudtaskspb"
	"github.com/google/go-cmp/cmp"
	gqueue "golang.org/x/oscar/internal/queue"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/durationpb"
)

type testTask struct {
	name   string
	path   string
	params string
}

func (t *testTask) Name() string   { return t.name }
func (t *testTask) Path() string   { return t.path }
func (t *testTask) Params() string { return t.params }

func TestNewTaskID(t *testing.T) {
	for _, test := range []struct {
		name, path, params string
		want               string
	}{
		{
			"m@v1.2", "path", "params",
			"m_v1_2-31026413",
		},
		{
			"µπΩ/github.com@v2.3.4-ß", "p", "",
			"_00b5_03c0_03a9_-github_com_v2_3_4-_00df-148de9c5",
		},
	} {
		tt := &testTask{test.name, test.path, test.params}
		got := newTaskID(tt)
		if got != test.want {
			t.Errorf("%v: got %s, want %s", tt, got, test.want)
		}
	}
}

func TestNewTaskRequest(t *testing.T) {
	m := &gqueue.Metadata{
		Project:        "Project",
		QueueName:      "queueID",
		QueueURL:       "http://1.2.3.4:8000",
		ServiceAccount: "sa",
		Location:       "us-central1",
	}
	want := &taskspb.CreateTaskRequest{
		Parent: "projects/Project/locations/us-central1/queues/queueID",
		Task: &taskspb.Task{
			Name:             "projects/Project/locations/us-central1/queues/queueID/tasks/name-404c649f-suf",
			DispatchDeadline: durationpb.New(maxCloudTasksTimeout),
			MessageType: &taskspb.Task_HttpRequest{
				HttpRequest: &taskspb.HttpRequest{
					HttpMethod: taskspb.HttpMethod_POST,
					Url:        "http://1.2.3.4:8000/bisect?repo=golang",
					AuthorizationHeader: &taskspb.HttpRequest_OidcToken{
						OidcToken: &taskspb.OidcToken{
							ServiceAccountEmail: "sa",
						},
					},
				},
			},
		},
	}
	gcp, err := newGCP(nil, m)
	if err != nil {
		t.Fatal(err)
	}
	opts := &gqueue.Options{
		TaskNameSuffix: "suf",
	}
	sreq := &testTask{
		name:   "name",
		path:   "bisect",
		params: "repo=golang",
	}
	got, err := gcp.newTaskRequest(sreq, opts)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("mismatch (-want, +got):\n%s", diff)
	}
}

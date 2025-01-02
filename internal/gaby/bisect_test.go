// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/oscar/internal/bisect"
	"golang.org/x/oscar/internal/github"
)

func TestParseBisectTrigger(t *testing.T) {
	for _, tc := range []struct {
		name string
		e    *github.WebhookIssueCommentEvent
		want *bisect.Request
		err  bool
	}{
		{
			name: "no bisect directive",
			e: &github.WebhookIssueCommentEvent{
				Comment: github.Comment{
					Body: "body1",
				},
			},
			want: nil,
			err:  false,
		},
		{
			name: "missing bisect command",
			e: &github.WebhookIssueCommentEvent{
				Comment: github.Comment{
					Body: "@gabyhelp",
				},
			},
			want: nil,
			err:  false,
		},
		{
			name: "bisect directive not first",
			e: &github.WebhookIssueCommentEvent{
				Comment: github.Comment{
					Body: strings.Join([]string{
						"some line",
						"@gabyhelp bisect",
					}, "\n"),
				},
			},
			want: nil,
			err:  false,
		},
		{
			name: "wrong number of directive args",
			e: &github.WebhookIssueCommentEvent{
				Comment: github.Comment{
					Body: "@gabyhelp bisect go1.22.0 go1.22.1 go1.22.2",
				},
			},
			want: nil,
			err:  true,
		},
		{
			name: "insufficient number of directive args",
			e: &github.WebhookIssueCommentEvent{
				Comment: github.Comment{
					Body: "@gabyhelp bisect go1.22.0",
				},
			},
			want: nil,
			err:  true,
		},
		{
			name: "no regression body",
			e: &github.WebhookIssueCommentEvent{
				Comment: github.Comment{
					Body: "@gabyhelp bisect",
				},
			},
			want: nil,
			err:  true,
		},
		{
			name: "correct with no directive args",
			e: &github.WebhookIssueCommentEvent{
				Comment: github.Comment{
					Body: strings.Join([]string{
						"@gabyhelp bisect",
						"```",
						"some code",
						"```",
					}, "\n"),
				},
			},
			want: &bisect.Request{
				Fail: "master",
				Pass: "go1.22.0",
				Repo: "https://go.googlesource.com/go",
				Body: "some code",
			},
			err: false,
		},
		{
			name: "correct with directive args",
			e: &github.WebhookIssueCommentEvent{
				Comment: github.Comment{
					Body: strings.Join([]string{
						"@gabyhelp bisect go1.22.1 go1.22.2",
						"```",
						"some code",
						"```",
					}, "\n"),
				},
			},
			want: &bisect.Request{
				Fail: "go1.22.1",
				Pass: "go1.22.2",
				Repo: "https://go.googlesource.com/go",
				Body: "some code",
			},
			err: false,
		},
		{
			name: "wrong order for directive and regression body",
			e: &github.WebhookIssueCommentEvent{
				Comment: github.Comment{
					Body: strings.Join([]string{
						"```",
						"some code",
						"```",
						"@gabyhelp bisect go1.22.1 go1.22.2",
					}, "\n"),
				},
			},
			want: nil,
			err:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseBisectTrigger(tc.e)
			if tc.err && err == nil {
				t.Error("got no error; want some")
			} else if diff := cmp.Diff(got, tc.want); diff != "" {
				t.Errorf("request mismatch (-got +want):\n%s", diff)
			}
		})
	}
}

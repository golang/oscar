// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/oscar/internal/actions"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
	"rsc.io/ordered"
)

func TestTimeOrDuration(t *testing.T) {
	for _, test := range []struct {
		endpoint endpoint
		wantTime time.Time
		wantDur  time.Duration
	}{
		{
			endpoint: endpoint{Radio: "fixed"},
			wantTime: time.Time{},
		},
		{
			endpoint: endpoint{Radio: "date", Date: "2018-01-02T09:11"},
			wantTime: time.Date(2018, 1, 2, 9, 11, 0, 0, time.Local),
		},
		{
			endpoint: endpoint{Radio: "dur", DurNum: "3", DurUnit: "hours"},
			wantDur:  3 * time.Hour,
		},
	} {
		gotTime, gotDur, err := test.endpoint.timeOrDuration(time.Local)
		if err != nil {
			t.Fatal(err)
		}
		if !gotTime.Equal(test.wantTime) || gotDur != test.wantDur {
			t.Errorf("%+v: got (%s, %s), want (%s, %s)",
				test.endpoint, gotTime, gotDur, test.wantTime, test.wantDur)
		}
	}
}

func TestTimes(t *testing.T) {
	dt := func(year int, month time.Month, day, hour, minute int) time.Time {
		return time.Date(year, month, day, hour, minute, 0, 0, time.Local)
	}

	now := dt(2024, 9, 10, 0, 0)

	for _, test := range []struct {
		start, end         endpoint
		wantStart, wantEnd time.Time
	}{
		{
			start:     endpoint{Radio: "fixed"},
			end:       endpoint{Radio: "fixed"},
			wantStart: time.Time{},
			wantEnd:   now,
		},
		{
			start:     endpoint{Radio: "dur", DurNum: "1", DurUnit: "hours"},
			end:       endpoint{Radio: "date", Date: "2001-11-12T04:00"},
			wantStart: dt(2001, 11, 12, 3, 0),
			wantEnd:   dt(2001, 11, 12, 4, 0),
		},
		{
			start:     endpoint{Radio: "date", Date: "2001-11-12T04:00"},
			end:       endpoint{Radio: "dur", DurNum: "1", DurUnit: "hours"},
			wantStart: dt(2001, 11, 12, 4, 0),
			wantEnd:   dt(2001, 11, 12, 5, 0),
		},
		{
			start:     endpoint{Radio: "date", Date: "2001-11-12T04:00"},
			end:       endpoint{Radio: "date", Date: "2002-01-02T11:21"},
			wantStart: dt(2001, 11, 12, 4, 0),
			wantEnd:   dt(2002, 1, 2, 11, 21),
		},
	} {
		gotStart, gotEnd, err := times(test.start, test.end, now, time.Local)
		if err != nil {
			t.Fatal(err)
		}
		if !gotStart.Equal(test.wantStart) || !gotEnd.Equal(test.wantEnd) {
			t.Errorf("times(%+v, %+v):\ngot  (%s, %s)\nwant (%s, %s)",
				test.start, test.end, gotStart, gotEnd, test.wantStart, test.wantEnd)
		}
	}
}

func TestActionTemplate(t *testing.T) {
	page := actionLogPage{
		Start:     endpoint{DurNum: "3", DurUnit: "days"},
		StartTime: "whatevs",
		Entries: []*actions.Entry{
			{
				Created: time.Now(),
				Key:     ordered.Encode("P", 22),
				Action:  []byte(`{"Project": "P", "Issue":22, "Fix": "fix"}`),
			},
		},
	}
	page.setCommonPage()
	b, err := Exec(actionLogPageTmpl, &page)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	wants := []string{
		`<option value="days" selected>days</option>`,
		`Project`,
		`Issue`,
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("did not find %q", w)
		}
	}
	if t.Failed() {
		t.Log(got)
	}
}

type testActioner struct {
	actions.Actioner
}

func (testActioner) Run(context.Context, []byte) ([]byte, error) { return nil, nil }

func TestActionsBetween(t *testing.T) {
	db := storage.MemDB()
	g := &Gaby{slog: testutil.Slogger(t), db: db}
	before := actions.Register("actionlog", testActioner{})
	start := time.Now()
	before(db, []byte{1}, nil, false)
	end := time.Now()
	time.Sleep(100 * time.Millisecond)
	before(db, []byte{2}, nil, false)

	got := g.actionsBetween(start, end, func(context.Context, *actions.Entry) bool { return true })
	if len(got) != 1 {
		t.Errorf("got %d entries, want 1", len(got))
	}
}

func TestActionFilter(t *testing.T) {
	entries := []*actions.Entry{
		{Kind: "a", Error: ""},
		{Kind: "b", Error: ""},
		{Kind: "a", Error: "a bad error"},
		{Kind: "a", Error: "meh"},
		{Kind: "b", Error: "bad"},
		{Kind: "a", ApprovalRequired: true},
		{Kind: "b", ApprovalRequired: true},
	}

	ctx := context.Background()
	applyFilter := func(f func(context.Context, *actions.Entry) bool) []*actions.Entry {
		var res []*actions.Entry
		for _, e := range entries {
			if f(ctx, e) {
				res = append(res, e)
			}
		}
		return res
	}

	for _, tc := range []struct {
		in   string
		want func(context.Context, *actions.Entry) bool
	}{
		{"", func(context.Context, *actions.Entry) bool { return true }},
		{
			"Kind=a",
			func(ctx context.Context, e *actions.Entry) bool { return e.Kind == "a" },
		},
		{
			"Kind=a Error:bad",
			func(ctx context.Context, e *actions.Entry) bool {
				return e.Kind == "a" && strings.Contains(e.Error, "bad")
			},
		},
		{
			"ApprovalRequired=true",
			func(ctx context.Context, e *actions.Entry) bool { return e.ApprovalRequired },
		},
		{
			"ApprovalRequired=true OR Kind=b",
			func(ctx context.Context, e *actions.Entry) bool {
				return e.ApprovalRequired || e.Kind == "b"
			},
		},
	} {
		gotf, err := newFilter(tc.in)
		if err != nil {
			t.Fatal(err)
		}
		got := applyFilter(gotf)
		want := applyFilter(tc.want)
		if !slices.Equal(got, want) {
			t.Errorf("%q:\ngot  %v\nwant %v", tc.in, got, want)
		}
	}
}

func TestDoActionDecision(t *testing.T) {
	const kind = "actionlog"
	db := storage.MemDB()
	before := actions.Register(kind, testActioner{})

	// Register some actions.
	var (
		noApproveKey   = []byte{1} // approval not required
		approveKey     = []byte{2} // will be approved
		denyKey        = []byte{3} // wil be denied
		approveDenyKey = []byte{4} // will be approved, then denied
	)
	before(db, noApproveKey, nil, false)
	before(db, approveKey, nil, true)
	before(db, denyKey, nil, true)
	before(db, approveDenyKey, nil, true)
	actions.AddDecision(db, kind, approveDenyKey, actions.Decision{Approved: true})

	g := &Gaby{slog: testutil.Slogger(t), db: db}
	for _, tc := range []struct {
		name           string
		key            []byte // key for the action, value for "key" query param
		decision       string // value for "decision" query param
		approvedBefore bool   // sanity check: was this action already approved?
		wantApproved   bool   // is the action approved aferwards?
		wantErr        string // if non-empty, error should contain this
	}{
		{
			name:           "not required",
			key:            noApproveKey,
			decision:       "Approve",
			approvedBefore: true,
			wantErr:        "does not require approval",
		},
		{
			name:         "deny",
			key:          denyKey,
			decision:     "Deny",
			wantApproved: false,
		},
		{
			name:         "approve",
			key:          approveKey,
			decision:     "Approve",
			wantApproved: true,
		},
		{
			name: "redeny",
			// You can deny a previously approved request, though the UI doesn't
			// make it possible (no buttons will appear).
			key:            approveDenyKey,
			decision:       "Deny",
			approvedBefore: true,
			wantApproved:   false,
		},
		{
			name:     "nonexistent",
			key:      []byte("does not exist"),
			decision: "Deny",
			wantErr:  "cannot find",
		},
		{
			name:     "bad decision",
			key:      denyKey,
			decision: "maybe",
			wantErr:  "invalid decision",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if entry, ok := actions.Get(db, kind, tc.key); ok {
				if g, w := entry.Approved(), tc.approvedBefore; g != w {
					t.Fatalf("approved before: got %t, want %t", g, w)
				}
			}
			url := fmt.Sprintf("/action-decision?kind=%s&key=%s&decision=%s",
				kind, hex.EncodeToString(tc.key), tc.decision)
			r := httptest.NewRequest("GET", url, nil)
			_, _, err := g.doActionDecision(r)
			if err != nil {
				if tc.wantErr == "" {
					t.Fatal(err)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("got %q, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			entry, ok := actions.Get(db, kind, tc.key)
			if !ok {
				t.Fatal("action not found")
			}
			if g, w := entry.Approved(), tc.wantApproved; g != w {
				t.Errorf("approved: got %t, want %t", g, w)
			}
		})
	}
}

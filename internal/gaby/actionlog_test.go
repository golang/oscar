// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestTimeOrDuration(t *testing.T) {
	now := time.Now()
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
		gotTime, gotDur, err := test.endpoint.timeOrDuration(now)
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
		gotStart, gotEnd, err := times(test.start, test.end, now)
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
	var buf bytes.Buffer
	page := actionLogPage{
		Start: endpoint{DurNum: "3", DurUnit: "days"},
	}
	if err := actionLogPageTmpl.Execute(&buf, page); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	wants := []string{
		`<option value="days" selected>days</option>`,
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

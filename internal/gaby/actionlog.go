// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/safehtml"
	"github.com/google/safehtml/template"
	"golang.org/x/oscar/internal/actions"
	"golang.org/x/oscar/internal/filter"
	"golang.org/x/oscar/internal/storage"
)

// actionLogPage is the data for the action log HTML template.
type actionLogPage struct {
	CommonPage

	Start, End         endpoint
	StartTime, EndTime string // formatted times that the endpoints describe
	Filter             string
	Entries            []*actions.Entry
}

// An endpoint holds the values for a UI component for selecting a point in time.
type endpoint struct {
	Radio   string // "fixed", "dur" or "date"
	Date    string // date and time, see below for format
	DurNum  string // integer
	DurUnit string // "hours", "minutes", etc.
}

func (g *Gaby) handleActionLog(w http.ResponseWriter, r *http.Request) {
	data, status, err := g.doActionLog(r)
	if err != nil {
		http.Error(w, err.Error(), status)
	} else {
		_, _ = w.Write(data)
	}
}

func (g *Gaby) doActionLog(r *http.Request) (content []byte, status int, err error) {
	var page actionLogPage

	// Fill in the endpoint values from the form on the page.
	page.Filter = r.FormValue("filter")
	page.Start.formValues(r, "start")
	page.End.formValues(r, "end")

	browserTimezone := r.FormValue("timezone")
	loc, err := time.LoadLocation(browserTimezone)
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("no timezone named %q", browserTimezone)
	}

	startTime, endTime, err := times(page.Start, page.End, time.Now())
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	if startTime.After(time.Now()) {
		return nil, http.StatusBadRequest, errors.New("start time is after the current time")
	}
	startTime = startTime.In(loc)
	endTime = endTime.In(loc)

	// Retrieve and display entries if something was set.
	if r.FormValue("start") != "" {
		filter, err := newFilter(page.Filter)
		if err != nil {
			return nil, http.StatusBadRequest, fmt.Errorf("invalid filter: %v", err)
		}
		page.Entries = g.actionsBetween(startTime, endTime, filter)
		for _, e := range page.Entries {
			e.Created = e.Created.In(loc)
			e.Done = e.Done.In(loc)
		}

		if startTime.IsZero() {
			page.StartTime = "the beginning of time"
		} else {
			page.StartTime = startTime.Format(time.DateTime)
		}
		page.EndTime = endTime.Format(time.DateTime)
	}

	// Set defaults: from 1 hour ago to now.
	if page.Start.Radio == "" {
		page.Start.Radio = "dur"
		page.Start.DurNum = "1"
		page.Start.DurUnit = "hours"
	}
	if page.End.Radio == "" {
		page.End.Radio = "fixed"
	}

	page.setCommonPage()

	b, err := Exec(actionLogPageTmpl, &page)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return b, http.StatusOK, nil
}

func (p *actionLogPage) setCommonPage() {
	p.CommonPage = *p.toCommonPage()
}

func (p *actionLogPage) toCommonPage() *CommonPage {
	return &CommonPage{
		ID:          actionlogID,
		Description: "Browse actions taken by Oscar.",
		Form: Form{
			// Unset because the action log page defines its form inputs
			// directly in an HTML template.
			Inputs:     nil,
			SubmitText: "display",
		},
	}
}

// formValues populates an endpoint from the values in the form.
func (e *endpoint) formValues(r *http.Request, prefix string) {
	e.Radio = r.FormValue(prefix)
	e.Date = r.FormValue(prefix + "-date")
	e.DurNum = r.FormValue(prefix + "-dur-num")
	e.DurUnit = r.FormValue(prefix + "-dur-unit")
}

// times computes the start and end times from the endpoint controls.
func times(start, end endpoint, now time.Time) (startTime, endTime time.Time, err error) {
	var ztime time.Time
	st, sd, err1 := start.timeOrDuration()
	et, ed, err2 := end.timeOrDuration()
	if err := errors.Join(err1, err2); err != nil {
		return ztime, ztime, err
	}
	if sd != 0 && ed != 0 {
		return ztime, ztime, errors.New("both endpoints can't be durations")
	}
	// TODO(jba): times should be local to the user, not the Gaby server.

	// The "fixed" choice always returns a zero time, but for the end endpoint is should be now.
	if et.IsZero() && ed == 0 {
		et = now
	}

	// Add a duration to a time.
	if sd != 0 {
		st = et.Add(-sd)
	}
	if ed != 0 {
		et = st.Add(ed)
	}
	if et.Before(st) {
		return ztime, ztime, errors.New("end time before start time")
	}
	return st, et, nil
}

// timeOrDuration returns the time or duration described by the endpoint's controls.
// If the controls aren't set, it returns the zero time.
func (e *endpoint) timeOrDuration() (time.Time, time.Duration, error) {
	var ztime time.Time
	switch e.Radio {
	case "", "fixed":
		// A fixed time: start at the beginning of time, end at now.
		// That choice is left to the caller.
		return ztime, 0, nil
	case "date":
		// Format described in
		// https://developer.mozilla.org/en-US/docs/Web/HTML/Date_and_time_formats#local_date_and_time_strings
		// TODO(jba): times should be local to the user, not the Gaby server.
		t, err := time.ParseInLocation("2006-01-02T15:04", e.Date, time.Local)
		if err != nil {
			return ztime, 0, err
		}
		return t, 0, nil
	case "dur":
		// A duration after a start time, or before an end time.
		if e.DurNum == "" {
			return ztime, 0, errors.New("missing duration value")
		}
		num, err := strconv.Atoi(e.DurNum)
		if err != nil {
			return ztime, 0, err
		}
		if num <= 0 {
			return ztime, 0, errors.New("non-positive duration")
		}
		var unit time.Duration
		switch e.DurUnit {
		case "minutes":
			unit = time.Minute
		case "hours":
			unit = time.Hour
		case "days":
			unit = 24 * time.Hour
		case "weeks":
			unit = 7 * 24 * time.Hour
		default:
			return ztime, 0, fmt.Errorf("bad duration unit %q", e.DurUnit)

		}
		return ztime, time.Duration(num) * unit, nil
	default:
		return ztime, 0, fmt.Errorf("bad radio button value %q", e.Radio)
	}
}

// actionsBetween returns the action entries between start and end, inclusive.
func (g *Gaby) actionsBetween(start, end time.Time, filter func(*actions.Entry) bool) []*actions.Entry {
	var es []*actions.Entry
	// Scan entries created in [start, end].
	for e := range actions.ScanAfter(g.slog, g.db, start.Add(-time.Nanosecond), nil) {
		if e.Created.After(end) {
			break
		}
		if filter(e) {
			es = append(es, e)
		}
	}
	return es
}

func newFilter(s string) (func(*actions.Entry) bool, error) {
	if s == "" {
		return func(*actions.Entry) bool { return true }, nil
	}
	expr, err := filter.ParseFilter(s)
	if err != nil {
		return nil, err
	}
	ev, problems := filter.Evaluator[actions.Entry](expr, nil)
	if len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "\n"))
	}
	return func(e *actions.Entry) bool {
		if e == nil {
			return false
		}
		return ev(*e)
	}, nil
}

// fmtTime formats a time for display on the action log page.
func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format(time.DateTime)
}

// fmtValue tries to produce a readable string from a []byte.
// If the slice contains JSON, it displays it as a multi-line indented string.
// Otherwise it simply converts the []byte to a string.
func fmtValue(b []byte) string {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return string(b)
	}
	r, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Sprintf("ERROR: %s", err)
	}
	return string(r)
}

var actionLogPageTmpl = newTemplate(actionLogTmplFile, template.FuncMap{
	"fmttime": fmtTime,
	"fmtkey":  func(key []byte) string { return storage.Fmt(key) },
	"fmtval":  fmtValue,
	"safeid": func(s string) safehtml.Identifier {
		return safehtml.IdentifierFromConstantPrefix("id", s)
	},
})

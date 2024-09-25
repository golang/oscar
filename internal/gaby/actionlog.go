// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/safehtml/template"
	"golang.org/x/oscar/internal/actions"
)

// actionLogPage is the data for the action log HTML template.
type actionLogPage struct {
	Start, End         endpoint
	StartTime, EndTime string // formatted times that the endpoints describe
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
	page.Start.formValues(r, "start")
	page.End.formValues(r, "end")

	startTime, endTime, err := times(page.Start, page.End, time.Now())
	if err != nil {
		return nil, http.StatusBadRequest, err
	}

	// Display a table heading, but only if something was set.
	if r.FormValue("start") != "" {
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

	var buf bytes.Buffer
	if err := actionLogPageTmpl.Execute(&buf, page); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return buf.Bytes(), http.StatusOK, nil
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
	st, sd, err1 := start.timeOrDuration(now)
	et, ed, err2 := end.timeOrDuration(now)
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
func (e *endpoint) timeOrDuration(now time.Time) (time.Time, time.Duration, error) {
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
		t, err := time.ParseInLocation("2006-01-02T03:04", e.Date, time.Local)
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

// Embed the action template into the binary.
// We must use the FS form in order to make it trusted with the
// github.com/google/safehtml/template API.

//go:embed actionlog.tmpl
var actionLogFS embed.FS

const actionLogTmplFile = "actionlog.tmpl"

// The template name must match the filename.
var actionLogPageTmpl = template.Must(template.New(actionLogTmplFile).
	ParseFS(template.TrustedFSFromEmbed(actionLogFS), actionLogTmplFile))

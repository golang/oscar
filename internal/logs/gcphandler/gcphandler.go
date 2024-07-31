// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gcphandler implements a slog.Handler that works
// with the Google Cloud Platform's logging service.
// It always writes to stderr, so it is most suitable for
// programs that run on a managed service that treats JSON
// lines written to stderr as logs, like Cloud Run and AppEngine.
package gcphandler

import (
	"io"
	"log/slog"
	"os"
	"time"
)

// New creates a new [slog.Handler] for GCP logging.
func New(level slog.Leveler) slog.Handler {
	return newHandler(level, os.Stderr)
}

// newHandler is for testing.
func newHandler(level slog.Leveler, w io.Writer) slog.Handler {
	return slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: replaceAttr,
	})
}

// If testTime is non-zero, replaceAttr will use it as the time.
var testTime time.Time

// replaceAttr uses GCP names for certain fields.
// It also formats times in the way that GCP expects.
// See https://cloud.google.com/logging/docs/agent/logging/configuration#special-fields.
func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	switch a.Key {
	case "time":
		if a.Value.Kind() == slog.KindTime {
			tm := a.Value.Time()
			if !testTime.IsZero() {
				tm = testTime
			}
			a.Value = slog.StringValue(tm.Format(time.RFC3339))
		}
	case "msg":
		a.Key = "message"
	case "level":
		a.Key = "severity"
	case "traceID":
		a.Key = "logging.googleapis.com/trace"
	}
	return a
}

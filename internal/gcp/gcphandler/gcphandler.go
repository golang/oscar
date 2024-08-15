// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gcphandler implements a [slog.Handler] that works
// with the Google Cloud Platform's logging service.
// It always writes to stderr, so it is most suitable for
// programs that run on a managed service that treats JSON
// lines written to stderr as logs, like Cloud Run and AppEngine.
package gcphandler

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// New creates a new [slog.Handler] for GCP logging.
// It follows [GCP's logging specification] by modifying
// slog defaults:
//   - It replaces the key "msg" with "message".
//   - It replaces the key "level" with "severity".
//   - It replaces the key "traceID" with "logging.googleapis.com/trace.".
//   - It replaces the value for the "time" key with an RFC3339-formatted string.
//
// It also appends some attributes to the message, because the GCP log viewer
// requires two clicks to get from the main log view to the attributes.
//
// [GCP's logging specification]: https://cloud.google.com/logging/docs/agent/logging/configuration#special-fields
func New(level slog.Leveler) slog.Handler {
	return newHandler(level, os.Stderr)
}

// The maximum length of a message after appending attributes.
// Var for testing.
var maxMessageLength = 100

// newHandler is for testing.
func newHandler(level slog.Leveler, w io.Writer) slog.Handler {
	jh := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: replaceAttr,
	})
	return &handler{jh}
}

// If testTime is non-zero, replaceAttr will use it as the time.
var testTime time.Time

// replaceAttr uses GCP names for certain fields.
// It also formats times in the way that GCP expects.
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

// handler is a [slog.Handler] that appends some attributes
// to the message, but otherwise behaves like its underlying
// handler.
type handler struct {
	h slog.Handler
}

func (h *handler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.h.Enabled(ctx, lvl)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &handler{h: h.h.WithAttrs(attrs)}
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{h: h.h.WithGroup(name)}
}

// Handle implements [slog.Handler] by appending the first attributes
// to the message until a limit is reached, then calling the underlying
// handler.
func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		s := a.String()
		if b.Len()+1+len(s) > maxMessageLength {
			return false
		}
		b.WriteByte(' ')
		b.WriteString(s)
		return true
	})
	r.Message = b.String()
	return h.h.Handle(ctx, r)
}

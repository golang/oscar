// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package testutil implements various testing utilities.
package testutil

import (
	"bytes"
	"io"
	"log/slog"
	"testing"
)

// LogWriter returns an [io.Writer] that logs each Write using t.Log.
func LogWriter(t *testing.T) io.Writer {
	return testWriter{t}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(b []byte) (int, error) {
	w.t.Logf("%s", b)
	return len(b), nil
}

// Slogger returns a [*slog.Logger] that writes each message
// using t.Log.
func Slogger(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(LogWriter(t), nil))
}

// SlogBuffer returns a [*slog.Logger] that writes each message to out.
func SlogBuffer() (lg *slog.Logger, out *bytes.Buffer) {
	var buf bytes.Buffer
	lg = slog.New(slog.NewTextHandler(&buf, nil))
	return lg, &buf
}

// Check calls t.Fatal(err) if err is not nil.
func Check(t *testing.T, err error) {
	if err != nil {
		t.Helper()
		t.Fatal(err)
	}
}

// Checker returns a check function that
// calls t.Fatal if err is not nil.
func Checker(t *testing.T) (check func(err error)) {
	return func(err error) {
		if err != nil {
			t.Helper()
			t.Fatal(err)
		}
	}
}

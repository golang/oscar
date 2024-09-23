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

// StopPanic runs f but silently recovers from any panic f causes.
// The normal usage is:
//
//	testutil.StopPanic(func() {
//		callThatShouldPanic()
//		t.Errorf("callThatShouldPanic did not panic")
//	})
func StopPanic(f func()) {
	defer func() { recover() }()
	f()
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

// Rot13 returns the rot13 of the input string (swap A-Ma-m with N-Zn-z).
func Rot13(s string) string {
	b := []byte(s)
	for i, x := range b {
		if 'A' <= x && x <= 'M' || 'a' <= x && x <= 'm' {
			b[i] = x + 13
		} else if 'N' <= x && x <= 'Z' || 'n' <= x && x <= 'z' {
			b[i] = x - 13
		}
	}
	return string(b)
}

// ExpectLog checks if the message is present in buf exactly n times,
// and calls t.Error if not.
func ExpectLog(t *testing.T, buf *bytes.Buffer, message string, n int) {
	t.Helper()
	if mentions := bytes.Count(buf.Bytes(), []byte(message)); mentions != n {
		t.Errorf("logs mention %q %d times, want %d mentions:\n%s", message, mentions, n, buf.Bytes())
	}
}

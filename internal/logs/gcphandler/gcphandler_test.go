// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcphandler

import (
	"bytes"
	"fmt"
	"log/slog"
	"testing"
	"time"
)

func TestHandler(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(newHandler(slog.LevelInfo, &buf))
	l = l.With("traceID", "tid")
	testTime = time.Now()
	l.Info("hello", slog.String("foo", "bar"), slog.Int("count", 7))
	got := buf.String()

	want := fmt.Sprintf(`{"time":%q,"severity":"INFO","message":"hello","logging.googleapis.com/trace":"tid","foo":"bar","count":7}`,
		testTime.Format(time.RFC3339))
	want += "\n"
	if got != want {
		t.Errorf("\ngot  %s\nwant %s", got, want)
	}
}

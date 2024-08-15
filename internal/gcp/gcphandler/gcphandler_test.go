// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcphandler

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestHandler(t *testing.T) {
	testTime = time.Date(2020, time.August, 20, 1, 2, 3, 0, time.UTC)
	defer func(old int) { maxMessageLength = old }(maxMessageLength)
	maxMessageLength = 10

	for _, test := range []struct {
		name    string
		message string
		attrs   []slog.Attr
		want    string
	}{
		{
			"all attrs fit",
			"hello",
			[]slog.Attr{slog.Int("c", 7)},
			`{"time":"2020-08-20T01:02:03Z","severity":"INFO","message":"hello c=7","logging.googleapis.com/trace":"tid","c":7}`,
		},
		{
			"some attrs fit",
			"hello",
			[]slog.Attr{slog.Int("c", 7), slog.Int("d", 1)},
			`{"time":"2020-08-20T01:02:03Z","severity":"INFO","message":"hello c=7","logging.googleapis.com/trace":"tid","c":7,"d":1}`,
		},
		{
			"message too long",
			"this is a long message",
			[]slog.Attr{slog.Int("c", 7)},
			`{"time":"2020-08-20T01:02:03Z","severity":"INFO","message":"this is a long message","logging.googleapis.com/trace":"tid","c":7}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := slog.New(newHandler(slog.LevelInfo, &buf))
			l = l.With("traceID", "tid")
			l.LogAttrs(context.Background(), slog.LevelInfo, test.message, test.attrs...)
			got := buf.String()
			want := test.want + "\n"
			if got != want {
				t.Errorf("\ngot  %s\nwant %s", got, want)
			}
		})
	}
}

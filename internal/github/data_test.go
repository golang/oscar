// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"testing"
	"time"
)

func TestMustParseTime(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want time.Time
	}{
		{"", time.Time{}},
		{"2015-08-05T01:55:29Z", time.Date(2015, 8, 5, 1, 55, 29, 0, time.UTC)},
		{"2024-11-11T18:41:58Z", time.Date(2024, 11, 11, 18, 41, 58, 0, time.UTC)},
	} {
		got := mustParseTime(tc.in)
		if !got.Equal(tc.want) {
			t.Errorf("%q: got %s, want %s", tc.in, got, tc.want)
		}
	}
}

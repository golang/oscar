// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"reflect"
	"testing"

	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
	"rsc.io/ordered"
)

var o = ordered.Encode

func TestParseOrdered(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []byte
	}{
		{"", nil},
		{"1", o(1)},
		{"moo", o("moo")},
		{"1 , moo  ", o(1, "moo")},
		{"foo, bar, Inf", o("foo", "bar", ordered.Inf)},
		{"-inf, 8", o(ordered.Rev(ordered.Inf), 8)},
	} {
		got := parseOrdered(tc.in)
		if !bytes.Equal(got, tc.want) {
			t.Errorf("%q: got %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestDBView(t *testing.T) {
	g := &Gaby{
		db:   storage.MemDB(),
		slog: testutil.Slogger(t),
	}
	for i := range 10 {
		g.db.Set(o("x", i), o(i))
	}

	for _, tc := range []struct {
		start, end []any
		want       *dbviewResult
	}{
		{[]any{"y"}, nil, nil},
		{[]any{"x", 3}, nil, &dbviewResult{Items: []item{{`("x", 3)`, "(3)"}}}},
		{[]any{"x", 3}, []any{"x", 5}, &dbviewResult{Items: []item{
			{`("x", 3)`, "(3)"},
			{`("x", 4)`, "(4)"},
			{`("x", 5)`, "(5)"},
		}}},
		{[]any{"x", 8}, []any{ordered.Inf}, &dbviewResult{Items: []item{
			{`("x", 8)`, "(8)"},
			{`("x", 9)`, "(9)"},
		}}},
	} {
		start := ordered.Encode(tc.start...)
		end := ordered.Encode(tc.end...)
		got, err := g.dbview(start, end, 100)
		if err != nil {
			t.Fatalf("start: %v, end: %v: %v", tc.start, tc.end, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("start: %v, end: %v:\ngot  %+v\nwant %+v", tc.start, tc.end, got, tc.want)
		}
	}
}

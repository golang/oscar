// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package storage

import (
	"math"
	"testing"

	"rsc.io/ordered"
)

func TestPanic(t *testing.T) {
	func() {
		defer func() {
			r := recover()
			if r.(string) != "msg key=val" {
				t.Errorf("panic value is not msg key=val:\n%s", r)
			}
		}()
		Panic("msg", "key", "val")
		t.Fatalf("did not panic")
	}()

}

func TestJSON(t *testing.T) {
	x := map[string]string{"a": "b"}
	js := JSON(x)
	want := `{"a":"b"}`
	if string(js) != want {
		t.Errorf("JSON(%v) = %#q, want %#q", x, js, want)
	}

	func() {
		defer func() {
			recover()
		}()
		JSON(math.NaN())
		t.Errorf("JSON(NaN) did not panic")
	}()
}

var fmtTests = []struct {
	data []byte
	out  string
}{
	{ordered.Encode(1, 2, 3), "(1, 2, 3)"},
	{[]byte(`"hello"`), "`\"hello\"`"},
	{[]byte("`hello`"), "\"`hello`\""},
}

func TestFmt(t *testing.T) {
	for _, tt := range fmtTests {
		out := Fmt(tt.data)
		if out != tt.out {
			t.Errorf("Fmt(%q) = %q, want %q", tt.data, out, tt.out)
		}
	}
}

func TestMemLocker(t *testing.T) {
	m := new(MemLocker)

	testDBLock(t, m)
}

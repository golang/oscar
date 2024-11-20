// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dbspec

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	dir := filepath.Join("some", "dir")
	for _, tc := range []struct {
		in      string
		want    Spec
		wantErr string // if non-empty, error should contain this
	}{
		{
			in:      "",
			wantErr: "unknown kind",
		},
		{
			in:      "dynamo:dbname",
			wantErr: "unknown kind",
		},
		{
			in:   "mem",
			want: Spec{Kind: "mem"},
		},
		{
			in:      "mem:",
			wantErr: "invalid",
		},
		{
			in: "mem~",
			want: Spec{
				Kind:      "mem",
				IsVector:  true,
				Namespace: "",
			},
		},
		{
			in: "mem~ns",
			want: Spec{
				Kind:      "mem",
				IsVector:  true,
				Namespace: "ns",
			},
		},
		{
			in: "pebble:" + dir,
			want: Spec{
				Kind:     "pebble",
				Location: dir,
			},
		},
		{
			in: `pebble:C:\WINDOWS\WORKS`,
			want: Spec{
				Kind:     "pebble",
				Location: `C:\WINDOWS\WORKS`,
			},
		},
		{
			in:      "pebble",
			wantErr: "missing directory",
		},
		{
			in:      "pebble:",
			wantErr: "missing directory",
		},
		{
			in: "pebble:" + dir + "~",
			want: Spec{
				Kind:      "pebble",
				Location:  dir,
				IsVector:  true,
				Namespace: "",
			},
		},
		{
			in: "pebble:" + dir + "~ns",
			want: Spec{
				Kind:      "pebble",
				Location:  dir,
				IsVector:  true,
				Namespace: "ns",
			},
		},
		{
			in:      "firestore",
			wantErr: "invalid firestore",
		},
		{
			in:      "firestore:",
			wantErr: "invalid firestore",
		},
		{
			in:      "firestore~",
			wantErr: "invalid firestore",
		},
		{
			in:      "firestore:proj",
			wantErr: "invalid firestore",
		},
		{
			in:      "firestore:,db",
			wantErr: "invalid firestore",
		},
		{
			in:   "firestore:proj,db",
			want: Spec{Kind: "firestore", Location: "proj", Name: "db"},
		},
		{
			in: "firestore:proj,db~",
			want: Spec{
				Kind:      "firestore",
				Location:  "proj",
				Name:      "db",
				IsVector:  true,
				Namespace: "",
			},
		},
		{
			in: "firestore:proj,db~ns",
			want: Spec{
				Kind:      "firestore",
				Location:  "proj",
				Name:      "db",
				IsVector:  true,
				Namespace: "ns",
			},
		},
	} {
		got, err := Parse(tc.in)
		if err != nil {
			if tc.wantErr == "" {
				t.Errorf("%q: %v", tc.in, err)
				continue
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%q: got %q, should contain %q", tc.in, err, tc.wantErr)
				continue
			}
		} else if g, w := *got, tc.want; g != w {
			t.Errorf("%q:\ngot  %#v\nwant %#v", tc.in, g, w)
		}
	}
}

func TestString(t *testing.T) {
	for _, tc := range []struct {
		in   Spec
		want string
	}{
		{
			in:   Spec{Kind: "unk"},
			want: `&dbspec.Spec{Kind:"unk", Location:"", Name:"", IsVector:false, Namespace:""}`,
		},
		{
			in:   Spec{Kind: "mem"},
			want: "mem",
		},
		{
			in:   Spec{Kind: "mem", IsVector: true, Namespace: "ns"},
			want: "mem~ns",
		},
		{
			in:   Spec{Kind: "pebble", Location: "dir"},
			want: "pebble:dir",
		},
		{
			in:   Spec{Kind: "firestore", Location: "p", Name: "o"},
			want: "firestore:p,o",
		},
	} {
		got := tc.in.String()
		if got != tc.want {
			t.Errorf("%#v: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

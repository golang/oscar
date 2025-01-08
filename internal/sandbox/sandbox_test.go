// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sandbox

import (
	"testing"
)

func TestValidate(t *testing.T) {
	// Validate doesn't actually run the sandbox, so we can test it.
	sb := New("testdata/bundle")
	sb.Runsc = "/usr/local/bin/runsc"
	if err := sb.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestNewSandboxID(t *testing.T) {
	for _, tc := range []struct {
		seed string
		cmd  *Cmd
		want string
	}{
		{"", &Cmd{Path: "go", Args: []string{"go", "test"}},
			"7e84b496d508810cbdd2c2e3d645a1ae76f1eac4da4680e5ef816fd68cd7f8ff"},
		{"go", &Cmd{Path: "go", Args: []string{"go", "test"}},
			"6e8fea9054dd3e01d6b63fa71183cd19982f96fe0f18d07c49b34a4f34f8c299"},
		{"go", &Cmd{Path: "go", Args: []string{"go", "test"}, Env: []string{"X=y"}},
			"3cc4f5b8966a9d902e66112e0b89752e0bd01adea43a72cdebda6b42cc8fd131"},
		{"go", &Cmd{Path: "go", Args: []string{"go", "test"}, Env: []string{"X=y"}, Dir: "/dir"},
			"7c1f9c1740e6dc79fd42201714e98cc18711a5c039a34717d501153dcfff62b8"},
	} {
		got := newSandboxID(tc.seed, tc.cmd)
		if got != tc.want {
			t.Errorf("got %s for %+v; want %s", got, tc.cmd, tc.want)
		}
	}
}

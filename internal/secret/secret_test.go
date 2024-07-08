// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package secret

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNetrc(t *testing.T) {
	file := filepath.Join(t.TempDir(), "netrc")
	if err := os.WriteFile(file, []byte(testNetrc), 0666); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NETRC", file)

	db := Netrc()
	if secret, ok := db.Get("missing"); secret != "" || ok != false {
		t.Errorf("Get(missing) = %q, %v, want %q, %v", secret, ok, "", false)
	}

	if secret, ok := db.Get("example.com"); secret != "u2:p2" || ok != true {
		t.Errorf("Get(example.com) = %q, %v, want %q, %v", secret, ok, "u2:p2", true)
	}

	func() {
		defer func() {
			recover()
		}()
		db.Set("name", "value")
		t.Errorf("Set did not panic")
	}()
}

var testNetrc = `
machine example.com login u1 password p1
machine missing login u password p and more
machine example.com login u2 password p2
`

func TestEmpty(t *testing.T) {
	db := Empty()
	if secret, ok := db.Get("missing"); secret != "" || ok != false {
		t.Errorf("Get(missing) = %q, %v, want %q, %v", secret, ok, "", false)
	}

	func() {
		defer func() {
			recover()
		}()
		db.Set("name", "value")
		t.Errorf("Set did not panic")
	}()
}

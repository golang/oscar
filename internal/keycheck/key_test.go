// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Test that API keys do not appear in any httprr logs in this repo.

package keycheck

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oscar/internal/testutil"
)

var bads = []string{
	"\nAuthorization:",
	"\nx-goog-api-key:",
	"\nX-Goog-Api-Key:",
}

func TestTestdata(t *testing.T) {
	check := testutil.Checker(t)
	err := filepath.WalkDir("../..", func(file string, d fs.DirEntry, err error) error {
		if strings.HasSuffix(file, ".httprr") {
			data, err := os.ReadFile(file)
			check(err)
			for _, bad := range bads {
				if bytes.Contains(data, []byte(bad)) {
					t.Errorf("%s contains %q", file, bad)
				}
			}
		}
		return nil
	})
	check(err)
}

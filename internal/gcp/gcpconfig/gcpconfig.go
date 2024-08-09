// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gcpconfig supports configuring Oscar programs
// for use with GCP.
package gcpconfig

import (
	"errors"
	"os"
	"testing"
)

// Project returns the Oscar GCP project ID from the environment, or an error
// if it is not provided.
func Project() (string, error) {
	if p := os.Getenv("OSCAR_PROJECT"); p != "" {
		return p, nil
	}
	return "", errors.New("OSCAR_PROJECT environment variable is not set")
}

// MustProject returns the result of [Project].
// If Project returns an error, it calls t.Fatal.
func MustProject(t *testing.T) string {
	p, err := Project()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

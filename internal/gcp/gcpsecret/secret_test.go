// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package implements the [internal/secret] package using Google Cloud
// Storage's Secret Manager service.
package gcpsecret

import (
	"context"
	"flag"
	"testing"
)

var project = flag.String("project", "", "project for testing")

func TestDB(t *testing.T) {
	if *project == "" {
		t.Skip("missing -project")
	}
	db, err := NewSecretDB(context.Background(), *project)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const (
		name   = "test"
		secret = "not-a-secret"
	)

	db.Set(name, secret)
	got, ok := db.Get(name)
	if !ok {
		t.Errorf("secret %q not defined", name)
	}
	if got != secret {
		t.Errorf("got %q, want %q", got, secret)
	}

	// Get of unknown name.
	if _, ok := db.Get("unknown"); ok {
		t.Error("got true, want false")
	}

	// Names with dots are disallowed by Secret Manager, but not by this DB.
	const dotName = "test.key"
	db.Set(dotName, "abc")
	if _, ok := db.Get(dotName); !ok {
		t.Errorf("secret %q not defined", dotName)
	}
}

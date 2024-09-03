// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"flag"
	"testing"
)

var projectID = flag.String("project", "", "project ID")

func TestIsAuthorized(t *testing.T) {
	userMap := map[string]role{
		"R": reader,
		"W": writer,
		"A": admin,
	}
	pathMap := map[string]role{
		"/r": reader,
		"/w": writer,
		"/a": admin,
	}

	for _, test := range []struct {
		user, path string
		want       bool
	}{
		{"R", "/r", true},
		{"R", "/w", false},
		{"W", "/a", false},
		{"W", "/r", true},
		{"X", "/r", true}, // every user is a reader
		{"X", "/w", false},
		{"X", "/x", false}, // unknown paths are admin
		{"W", "/x", false},
		{"A", "/x", true},
	} {
		got := isAuthorized(test.user, test.path, userMap, pathMap)
		if got != test.want {
			t.Errorf("user %s, path %s: got %t, want %t", test.user, test.path, got, test.want)
		}
	}
}

func TestFirestoreAuth(t *testing.T) {
	if *projectID == "" {
		t.Skip("no project ID")
	}
	ctx := context.Background()
	c, err := firestoreClient(ctx, *projectID)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	um, pm, err := readFirestoreRoles(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("user map: %+v", um)
	t.Logf("path map: %+v", pm)
}

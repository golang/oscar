// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"maps"
	"net/http"
	"testing"

	"golang.org/x/oscar/internal/httprr"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/testutil"
)

func TestLabels(t *testing.T) {
	const project = "jba/gabytest"
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)

	// Initial load.
	rr, err := httprr.Open("testdata/labels.httprr", http.DefaultTransport)
	check(err)
	rr.ScrubReq(Scrub)
	sdb := secret.Empty()
	if rr.Recording() {
		sdb = secret.Netrc()
	}
	c := New(lg, nil, sdb, rr.Client())
	labels, err := c.ListLabels(ctx, project)
	check(err)
	want := map[string]bool{"bug": true, "enhancement": true, "question": true}
	got := map[string]bool{}
	for _, lab := range labels {
		if want[lab.Name] {
			got[lab.Name] = true
		}
	}
	if !maps.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	lab := Label{Name: "gabytest", Description: "for testing gaby", Color: "888888"}
	const (
		// For EditLabel. The httprr package does not support two identical requests
		// in the same replay file, so we can't (1) get a label by name, (2) change
		// something other than the name, and (3) get it by name again to confirm the
		// change. We have to change the name.
		newName  = "gabytest2"
		newColor = "555555"
	)
	// Clean up from a possible earlier failed test. Ignore error; we don't care if
	// the labels don't exist.
	_ = c.deleteLabel(ctx, project, lab.Name)
	_ = c.deleteLabel(ctx, project, newName)
	check(c.CreateLabel(ctx, project, lab))
	gotlab, err := c.DownloadLabel(ctx, project, lab.Name)
	check(err)
	if gotlab != lab {
		t.Fatalf("got %+v, want %+v", gotlab, lab)
	}
	check(c.EditLabel(ctx, project, lab.Name, LabelChanges{NewName: newName, Color: newColor}))
	gotlab, err = c.DownloadLabel(ctx, project, newName)
	check(err)
	if gotlab != (Label{newName, lab.Description, newColor}) {
		t.Fatalf("got %+v, want %+v", gotlab, lab)
	}
}

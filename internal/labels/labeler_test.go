// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package labels

import (
	"context"
	"maps"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestSyncLabels(t *testing.T) {
	const project = "golang/go"
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	gh := github.New(lg, db, nil, nil)
	labeler := New(lg, nil, gh, "")
	m := map[string]github.Label{
		"A": {Name: "A", Description: "a", Color: "a"},
		"B": {Name: "B", Description: "", Color: "b"},
		"C": {Name: "C", Description: "c", Color: "c"},
		"D": {Name: "D", Description: "d", Color: "d"},
	}
	// Add the labels to the testing client in sorted order,
	// so ListLabels returns them in that order.
	for k := range maps.Keys(m) {
		gh.Testing().AddLabel(project, m[k])
	}

	cats := []Category{
		{Label: "A", Description: "a"},     // same as tracker
		{Label: "B", Description: "b"},     // set empty tracker description
		{Label: "C", Description: "other"}, // different descriptions
		// D in tracker but not in cats
		{Label: "E", Description: "e"}, // create
	}

	if err := labeler.syncLabels(context.Background(), project, cats); err != nil {
		t.Fatal(err)
	}

	want := []*github.TestingEdit{
		{
			// add B description
			Project:      project,
			Label:        github.Label{Name: "B"},
			LabelChanges: &github.LabelChanges{Description: "b"},
		},
		// create label E
		{Project: project, Label: github.Label{Name: "E", Description: "e", Color: labelColor}},
	}

	if diff := cmp.Diff(want, gh.Testing().Edits()); diff != "" {
		t.Errorf("mismatch (-want, got):\n%s", diff)
	}
}

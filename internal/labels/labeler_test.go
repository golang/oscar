// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package labels

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/oscar/internal/actions"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

func TestSyncLabels(t *testing.T) {
	const project = "golang/go"
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	gh := github.New(lg, db, nil, nil)
	labeler := New(lg, nil, gh, nil, "")
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

func TestRun(t *testing.T) {
	const project = "golang/go"
	ctx := context.Background()
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	gh := github.New(lg, db, nil, nil)
	gh.Testing().AddLabel(project, github.Label{Name: "bug"})
	gh.Testing().AddIssue(project, &github.Issue{
		Number: 1,
		Title:  "title",
		Body:   "body",
	})
	gh.Testing().AddIssue("other/project", &github.Issue{
		Number: 2,
		Title:  "title",
		Body:   "body",
	})
	cgen := llm.TestContentGenerator("test", func(context.Context, *llm.Schema, []llm.Part) (string, error) {
		return `{"CategoryName": "bug", "Explanation": "exp"}`, nil
	})
	l := New(lg, db, gh, cgen, "test")
	l.EnableProject(project)
	l.EnableLabels()

	check(l.Run(ctx))
	entries := slices.Collect(actions.ScanAfterDBTime(lg, db, 0, nil))
	if g := len(entries); g != 1 {
		t.Fatalf("got %d actions, want 1", g)
	}
	var got action
	check(json.Unmarshal(entries[0].Action, &got))
	if got.Issue.Number != 1 || !slices.Equal(got.NewLabels, []string{"Bug"}) {
		t.Errorf("got (%d, %v), want (1, [Bug])", got.Issue.Number, got.NewLabels)
	}

	// Second time, nothing should happen.
	check(l.Run(ctx))
	entries = slices.Collect(actions.ScanAfterDBTime(lg, db, entries[0].ModTime, nil))
	if g := len(entries); g != 0 {
		t.Fatalf("got %d actions, want 0", g)
	}
}
// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package labels

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"

	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/testutil"
)

func TestSyncLabels(t *testing.T) {
	m := map[string]github.Label{
		"A": {Name: "A", Description: "a", Color: "a"},
		"B": {Name: "B", Description: "", Color: "b"},
		"C": {Name: "C", Description: "c", Color: "c"},
		"D": {Name: "D", Description: "d", Color: "d"},
	}
	cats := []Category{
		{Label: "A", Description: "a"},     // same as tracker
		{Label: "B", Description: "b"},     // set empty tracker description
		{Label: "C", Description: "other"}, // different descriptions
		// D in tracker but not in cats
		{Label: "E", Description: "e"}, // create
	}
	tl := &testTrackerLabels{m}

	if err := syncLabels(context.Background(), testutil.Slogger(t), cats, tl); err != nil {
		t.Fatal(err)
	}

	want := map[string]github.Label{
		"A": {Name: "A", Description: "a", Color: "a"},
		"B": {Name: "B", Description: "b", Color: "b"}, // added B description
		"C": {Name: "C", Description: "c", Color: "c"},
		"D": {Name: "D", Description: "d", Color: "d"},
		"E": {Name: "E", Description: "e", Color: labelColor}, // added E
	}

	if got := tl.m; !maps.Equal(got, want) {
		t.Errorf("\ngot  %v\nwant %v", got, want)
	}
}

type testTrackerLabels struct {
	m map[string]github.Label
}

func (t *testTrackerLabels) CreateLabel(ctx context.Context, lab github.Label) error {
	if _, ok := t.m[lab.Name]; ok {
		return errors.New("label exists")
	}
	t.m[lab.Name] = lab
	return nil
}

func (t *testTrackerLabels) ListLabels(ctx context.Context) ([]github.Label, error) {
	return slices.Collect(maps.Values(t.m)), nil
}

func (t *testTrackerLabels) EditLabel(ctx context.Context, name string, changes github.LabelChanges) error {
	if changes.NewName != "" || changes.Color != "" {
		return errors.New("unsupported edit")
	}
	if lab, ok := t.m[name]; ok {
		lab.Description = changes.Description
		t.m[name] = lab
		return nil
	}
	return errors.New("not found")
}

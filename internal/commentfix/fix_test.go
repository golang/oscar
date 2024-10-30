// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package commentfix

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"text/template"
	"time"

	"golang.org/x/oscar/internal/actions"
	"golang.org/x/oscar/internal/diff"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
	"golang.org/x/tools/txtar"
)

var ctx = context.Background()

func TestTestdata(t *testing.T) {
	files, err := filepath.Glob("testdata/*.txt")
	testutil.Check(t, err)
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			a, err := txtar.ParseFile(file)
			testutil.Check(t, err)
			var f Fixer
			tmpl, err := new(template.Template).Parse(string(a.Comment))
			testutil.Check(t, err)
			testutil.Check(t, tmpl.Execute(io.Discard, &f))
			for i := 0; i+2 <= len(a.Files); {
				in := a.Files[i]
				out := a.Files[i+1]
				i += 2
				name := strings.TrimSuffix(in.Name, ".in")
				if name != strings.TrimSuffix(out.Name, ".out") {
					t.Fatalf("mismatched file pair: %s and %s", in.Name, out.Name)
				}
				t.Run(name, func(t *testing.T) {
					newBody, fixed := f.Fix(string(in.Data))
					if fixed != (newBody != "") {
						t.Fatalf("Fix() = %q, %v (len(newBody)=%d but fixed=%v)", newBody, fixed, len(newBody), fixed)
					}
					if newBody != string(out.Data) {
						t.Fatalf("Fix: incorrect output:\n%s", string(diff.Diff("want", []byte(out.Data), "have", []byte(newBody))))
					}
				})
			}
		})
	}
}

func TestPanics(t *testing.T) {
	testutil.StopPanic(func() {
		var f Fixer
		f.EnableEdits()
		t.Errorf("EnableEdits on zero Fixer did not panic")
	})

	testutil.StopPanic(func() {
		var f Fixer
		f.EnableProject("abc/xyz")
		t.Errorf("EnableProject on zero Fixer did not panic")
	})

	var f Fixer
	if err := f.Run(ctx); err == nil {
		t.Errorf("Run on zero Fixer did not err")
	}
}

func TestErrors(t *testing.T) {
	var f Fixer
	if err := f.AutoLink(`\`, ""); err == nil {
		t.Fatalf("AutoLink succeeded on bad regexp")
	}
	if err := f.ReplaceText(`\`, ""); err == nil {
		t.Fatalf("ReplaceText succeeded on bad regexp")
	}
	if err := f.ReplaceURL(`\`, ""); err == nil {
		t.Fatalf("ReplaceText succeeded on bad regexp")
	}
}

func TestGitHub(t *testing.T) {
	gh := testGitHub(t)
	db := storage.MemDB()
	lg := testutil.Slogger(t)
	check := testutil.Checker(t)

	checkNoLog := func() {
		t.Helper()
		if len(actionLogEntries(db)) > 0 {
			t.Fatal("actions were logged")
		}
	}

	// Check for action with too-new cutoff and edits disabled.
	// Finds nothing in the action log.
	f := New(lg, gh, db, "fixer1")
	f.SetStderr(testutil.LogWriter(t))
	f.EnableProject("rsc/tmp")
	f.SetTimeLimit(time.Date(2222, 1, 1, 1, 1, 1, 1, time.UTC))
	f.ReplaceText("cancelled", "canceled")
	check(f.Run(ctx))
	checkNoLog()

	// Check again with old enough cutoff.
	// Does not edit, does not advance cursor.
	f = New(lg, gh, db, "fixer1")
	f.SetStderr(testutil.LogWriter(t))
	f.EnableProject("rsc/tmp")
	f.SetTimeLimit(time.Time{})
	f.ReplaceText("cancelled", "canceled")
	check(f.Run(ctx))
	checkNoLog()

	// Run with too-new cutoff and edits enabled, should cause the issue to no
	// longer be visible again. But now the watcher advances.
	actions.ClearLogForTesting(t, db)
	f.SetTimeLimit(time.Date(2222, 1, 1, 1, 1, 1, 1, time.UTC))
	f.EnableEdits()
	check(f.Run(ctx))
	checkNoLog()

	// The watcher has passed the issue, so it won't be run even with the early cutoff.
	f.SetTimeLimit(time.Time{})
	check(f.Run(ctx))
	checkNoLog()

	// Write comment (now using fixer2 to avoid 'marked as old' in fixer1).
	f = New(lg, gh, db, "fixer2")
	f.SetStderr(testutil.LogWriter(t))
	f.EnableProject("rsc/tmp")
	f.ReplaceText("cancelled", "canceled")
	f.SetTimeLimit(time.Time{})
	f.EnableEdits()
	check(f.Run(ctx))
	actions.Run(ctx, lg, db)
	entries := actionLogEntries(db)
	if g, w := len(entries), 3; g != w {
		t.Fatalf("got %d entries, want %d", g, w)
	}
	for i, url := range []string{
		"https://api.github.com/repos/rsc/tmp/issues/18",
		"https://api.github.com/repos/rsc/tmp/issues/comments/10000000001",
		// no action for 19, a pull request
		"https://api.github.com/repos/rsc/tmp/issues/20",
	} {
		w := fmt.Sprintf(`{"URL":"%s"}`, url)
		if g := string(entries[i].Result); g != w {
			t.Errorf("entries[%d]: got %s, want %s", i, g, w)
		}
	}

	// Try again; comment should now be marked old in watcher.
	f = New(lg, gh, db, "fixer2")
	f.SetStderr(testutil.LogWriter(t))
	f.EnableProject("rsc/tmp")
	f.ReplaceText("cancelled", "canceled")
	f.EnableEdits()
	f.SetTimeLimit(time.Time{})
	check(f.Run(ctx))
	// There shouldn't be unexecuted actions.
	undone := filter(actionLogEntries(db),
		func(e *actions.Entry) bool { return !e.IsDone() })
	if len(undone) > 0 {
		t.Fatal("new actions were logged")
	}

	// Check that not enabling the project doesn't edit comments.
	f = New(lg, gh, db, "fixer3")
	f.SetStderr(testutil.LogWriter(t))
	f.EnableProject("xyz/tmp")
	f.ReplaceText("cancelled", "canceled")
	f.EnableEdits()
	f.SetTimeLimit(time.Time{})
	check(f.Run(ctx))
	entries = filter(actionLogEntries(db),
		func(e *actions.Entry) bool { return strings.HasSuffix(e.Kind, "fixer3") })
	if len(entries) > 0 {
		t.Fatal("new actions were logged")
	}

	// Check that when there's nothing to do, we still mark things old.
	f = New(lg, gh, db, "fixer4")
	f.SetStderr(testutil.LogWriter(t))
	f.EnableProject("rsc/tmp")
	f.ReplaceText("zyzzyva", "ZYZZYVA")
	f.EnableEdits()
	f.SetTimeLimit(time.Time{})
	check(f.Run(ctx))
	entries = filter(actionLogEntries(db),
		func(e *actions.Entry) bool { return strings.HasSuffix(e.Kind, "fixer4") })
	if len(entries) > 0 {
		t.Fatal("new actions were logged")
	}

	// Reverse the replacement and run again with same name; should not consider any comments.
	f = New(lg, gh, db, "fixer4")
	f.SetStderr(testutil.LogWriter(t))
	f.EnableProject("rsc/tmp")
	f.ReplaceText("c", "C")
	f.EnableEdits()
	f.SetTimeLimit(time.Time{})
	check(f.Run(ctx))
	entries = filter(actionLogEntries(db),
		func(e *actions.Entry) bool { return strings.HasSuffix(e.Kind, "fixer4") })
	if len(entries) > 0 {
		t.Fatal("new actions were logged")
	}
}

// runActions calls f.Run, then runs all the actions in the log.
func runActions(t *testing.T, f *Fixer) {
	t.Helper()
	if err := f.Run(ctx); err != nil {
		t.Fatal(err)
	}
	actions.Run(ctx, f.slog, f.db)
}

// funFix calls LogFixGitHubIssue, then runs all the actions in the log.
func runFix(t *testing.T, f *Fixer, project string, issue int64) {
	t.Helper()
	if err := f.LogFixGitHubIssue(ctx, project, issue); err != nil {
		t.Fatal(err)
	}
	actions.Run(ctx, f.slog, f.db)
}

// expectResultSubstrings checks that the results of the actions in the action log
// have the given substrings. Each result must match a substring, and
// there can be no actions left over. But the order of the actions doesn't matter.
func expectResultSubstrings(t *testing.T, db storage.DB, subs ...string) {
	t.Helper()
	wants := map[string]bool{}
	for _, s := range subs {
		wants[s] = true
	}
	entries := actionLogEntries(db)
	for _, e := range entries {
		g := string(e.Result)
		ok := false
		for w := range wants {
			if strings.Contains(g, w) {
				delete(wants, w)
				ok = true
				break
			}
		}
		if !ok {
			t.Fatalf("%s has no substring in %q", g, slices.Collect(maps.Keys(wants)))
		}
	}
	if len(wants) > 0 {
		t.Fatalf("action log results missing for these substrings: %q", slices.Collect(maps.Keys(wants)))
	}
}

func TestFixGitHubIssue(t *testing.T) {

	all := []string{"issues/18", "comments", "issues/20"}

	t.Run("basic", func(t *testing.T) {
		f, project, db := newFixer(t)
		runFix(t, f, project, 18)
		entries := actionLogEntries(db)
		if g, w := len(entries), 2; g != w {
			t.Fatalf("got %d entries, want %d", g, w)
		}
		expectResultSubstrings(t, db, "issues/18", "comments")
	})

	t.Run("twice", func(t *testing.T) {
		f, project, db := newFixer(t)
		runFix(t, f, project, 18)
		expectResultSubstrings(t, db, "issues/18", "comments")

		// Running Fix again doesn't change anything because fixes were
		// already applied.
		runFix(t, f, project, 18)
		expectResultSubstrings(t, db, "issues/18", "comments")
	})

	t.Run("fix-run", func(t *testing.T) {
		f, project, db := newFixer(t)
		runFix(t, f, project, 20)
		expectResultSubstrings(t, db, "issues/20")

		// Run still fixes issue 18 because FixGitHubIssue
		// doesn't modify Run's watcher.
		runActions(t, f)
		expectResultSubstrings(t, db, all...)
	})

	t.Run("fix-run-watcher", func(t *testing.T) {
		f, project, db := newFixer(t)
		runFix(t, f, project, 18)
		runFix(t, f, project, 20)
		expectResultSubstrings(t, db, all...)

		// Run sees that fixes have already been applied and advances
		// watcher.
		runActions(t, f)
		expectResultSubstrings(t, db, all...) // no change

		// Run doesn't do anything because its watcher has been advanced.
		runActions(t, f)
		expectResultSubstrings(t, db, all...)
	})

	t.Run("fix-run-concurrent", func(t *testing.T) {
		f, project, db := newFixer(t)
		var wg sync.WaitGroup

		wg.Add(1)
		go func() {
			runFix(t, f, project, 20)
			wg.Done()
		}()

		wg.Add(1)
		go func() {
			runActions(t, f)
			wg.Done()
		}()

		wg.Add(1)
		go func() {
			runFix(t, f, project, 18)
			wg.Done()
		}()

		wg.Wait()

		// Each action is attempted twice, but only happens once.
		expectResultSubstrings(t, db, all...)
	})

	t.Run("fix-concurrent", func(t *testing.T) {
		f, project, db := newFixer(t)

		var wg sync.WaitGroup

		n := 5
		wg.Add(n)
		for range n {
			go func() {
				runFix(t, f, project, 20)
				wg.Done()
			}()
		}

		wg.Wait()
		expectResultSubstrings(t, db, "issues/20")
	})
}

func TestActionMarshal(t *testing.T) {
	a := action{
		Project: "P",
		Issue:   3,
		IC: &issueOrComment{
			Issue: &github.Issue{
				URL: "u",
			},
		},
		Body: "b",
	}
	data, err := json.Marshal(&a)
	if err != nil {
		t.Fatal(err)
	}
	var g action
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, a) {
		t.Errorf("got %+v, want %+v", g, a)
	}
}

func newFixer(t *testing.T) (_ *Fixer, project string, db storage.DB) {
	gh := testGitHub(t)
	db = storage.MemDB()
	lg := testutil.Slogger(t)
	f := New(lg, gh, db, t.Name())
	f.SetStderr(testutil.LogWriter(t))
	project = "rsc/tmp"
	f.EnableProject(project)
	f.ReplaceText("cancelled", "canceled")
	f.SetTimeLimit(time.Time{})
	f.EnableEdits()
	return f, project, db
}

func testGitHub(t *testing.T) *github.Client {
	db := storage.MemDB()
	gh := github.New(testutil.Slogger(t), db, nil, nil)
	gh.Testing().AddIssue("rsc/tmp", &github.Issue{
		Number:    18,
		Title:     "spellchecking",
		Body:      "Contexts are cancelled.",
		CreatedAt: "2024-06-17T20:16:49-04:00",
		UpdatedAt: "2024-06-17T20:16:49-04:00",
	})

	// Ignored (pull request).
	gh.Testing().AddIssue("rsc/tmp", &github.Issue{
		Number:      19,
		Title:       "spellchecking",
		Body:        "Contexts are cancelled.",
		CreatedAt:   "2024-06-17T20:16:49-04:00",
		UpdatedAt:   "2024-06-17T20:16:49-04:00",
		PullRequest: new(struct{}),
	})

	gh.Testing().AddIssueComment("rsc/tmp", 18, &github.IssueComment{
		Body:      "No really, contexts are cancelled.",
		CreatedAt: "2024-06-17T20:16:49-04:00",
		UpdatedAt: "2024-06-17T20:16:49-04:00",
	})

	gh.Testing().AddIssueComment("rsc/tmp", 18, &github.IssueComment{
		Body:      "Completely unrelated.",
		CreatedAt: "2024-06-17T20:16:49-04:00",
		UpdatedAt: "2024-06-17T20:16:49-04:00",
	})

	gh.Testing().AddIssue("rsc/tmp", &github.Issue{
		Number:    20,
		Title:     "spellchecking 2",
		Body:      "Contexts are cancelled.",
		CreatedAt: "2024-06-17T20:16:49-04:00",
		UpdatedAt: "2024-06-17T20:16:49-04:00",
	})

	return gh
}

func actionLogEntries(db storage.DB) []*actions.Entry {
	return slices.Collect(actions.ScanAfter(nil, db, time.Time{}, nil))
}

func filter[S ~[]E, E any](s S, f func(E) bool) S {
	var r S
	for _, e := range s {
		if f(e) {
			r = append(r, e)
		}
	}
	return r
}

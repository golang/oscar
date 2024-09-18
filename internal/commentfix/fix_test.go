// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package commentfix

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"text/template"
	"time"

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
	// Check for comment with too-new cutoff and edits disabled.
	// Finds nothing but also no-op.
	gh := testGitHub(t)
	db := storage.MemDB()
	lg, buf := testutil.SlogBuffer()
	f := New(lg, gh, db, "fixer1")
	f.SetStderr(testutil.LogWriter(t))
	f.EnableProject("rsc/tmp")
	f.SetTimeLimit(time.Date(2222, 1, 1, 1, 1, 1, 1, time.UTC))
	f.ReplaceText("cancelled", "canceled")
	f.Run(ctx)
	// t.Logf("output:\n%s", buf)
	if bytes.Contains(buf.Bytes(), []byte("commentfix rewrite")) {
		t.Fatalf("logs mention rewrite of old comment:\n%s", buf.Bytes())
	}

	// Check again with old enough cutoff.
	// Finds comment but does not edit, does not advance cursor.
	f = New(lg, gh, db, "fixer1")
	f.SetStderr(testutil.LogWriter(t))
	f.EnableProject("rsc/tmp")
	f.SetTimeLimit(time.Time{})
	f.ReplaceText("cancelled", "canceled")
	f.Run(ctx)
	// t.Logf("output:\n%s", buf)
	if !bytes.Contains(buf.Bytes(), []byte("commentfix rewrite")) {
		t.Fatalf("logs do not mention rewrite of comment:\n%s", buf.Bytes())
	}
	if bytes.Contains(buf.Bytes(), []byte("editing github")) {
		t.Fatalf("logs incorrectly mention editing github:\n%s", buf.Bytes())
	}

	// Run with too-new cutoff and edits enabled, should make issue not seen again.
	buf.Truncate(0)
	f.SetTimeLimit(time.Date(2222, 1, 1, 1, 1, 1, 1, time.UTC))
	f.EnableEdits()
	f.Run(ctx)
	// t.Logf("output:\n%s", buf)
	if bytes.Contains(buf.Bytes(), []byte("commentfix rewrite")) {
		t.Fatalf("logs incorrectly mention rewrite of comment:\n%s", buf.Bytes())
	}

	f.SetTimeLimit(time.Time{})
	f.Run(ctx)
	// t.Logf("output:\n%s", buf)
	if bytes.Contains(buf.Bytes(), []byte("commentfix rewrite")) {
		t.Fatalf("logs incorrectly mention rewrite of comment:\n%s", buf.Bytes())
	}

	// Write comment (now using fixer2 to avoid 'marked as old' in fixer1).
	lg, buf = testutil.SlogBuffer()
	f = New(lg, gh, db, "fixer2")
	f.SetStderr(testutil.LogWriter(t))
	f.EnableProject("rsc/tmp")
	f.ReplaceText("cancelled", "canceled")
	f.SetTimeLimit(time.Time{})
	f.EnableEdits()
	f.Run(ctx)
	// t.Logf("output:\n%s", buf)
	if !bytes.Contains(buf.Bytes(), []byte("commentfix rewrite")) {
		t.Fatalf("logs do not mention rewrite of comment:\n%s", buf.Bytes())
	}
	if !bytes.Contains(buf.Bytes(), []byte("editing github")) {
		t.Fatalf("logs do not mention editing github:\n%s", buf.Bytes())
	}
	if !bytes.Contains(buf.Bytes(), []byte(`editing github" url=https://api.github.com/repos/rsc/tmp/issues/18`)) {
		t.Fatalf("logs do not mention editing issue body:\n%s", buf.Bytes())
	}
	if bytes.Contains(buf.Bytes(), []byte(`editing github" url=https://api.github.com/repos/rsc/tmp/issues/19`)) {
		t.Fatalf("logs incorrectly mention editing pull request body:\n%s", buf.Bytes())
	}
	if !bytes.Contains(buf.Bytes(), []byte(`editing github" url=https://api.github.com/repos/rsc/tmp/issues/comments/10000000001`)) {
		t.Fatalf("logs do not mention editing issue comment:\n%s", buf.Bytes())
	}
	if bytes.Contains(buf.Bytes(), []byte("ERROR")) {
		t.Fatalf("editing failed:\n%s", buf.Bytes())
	}

	// Try again; comment should now be marked old in watcher.
	lg, buf = testutil.SlogBuffer()
	f = New(lg, gh, db, "fixer2")
	f.SetStderr(testutil.LogWriter(t))
	f.EnableProject("rsc/tmp")
	f.ReplaceText("cancelled", "canceled")
	f.EnableEdits()
	f.SetTimeLimit(time.Time{})
	f.Run(ctx)
	// t.Logf("output:\n%s", buf)
	if bytes.Contains(buf.Bytes(), []byte("commentfix rewrite")) {
		t.Fatalf("logs incorrectly mention rewrite of comment:\n%s", buf.Bytes())
	}

	// Check that not enabling the project doesn't edit comments.
	lg, buf = testutil.SlogBuffer()
	f = New(lg, gh, db, "fixer3")
	f.SetStderr(testutil.LogWriter(t))
	f.EnableProject("xyz/tmp")
	f.ReplaceText("cancelled", "canceled")
	f.EnableEdits()
	f.SetTimeLimit(time.Time{})
	f.Run(ctx)
	// t.Logf("output:\n%s", buf)
	if bytes.Contains(buf.Bytes(), []byte("commentfix rewrite")) {
		t.Fatalf("logs incorrectly mention rewrite of comment:\n%s", buf.Bytes())
	}

	// Check that when there's nothing to do, we still mark things old.
	lg, buf = testutil.SlogBuffer()
	f = New(lg, gh, db, "fixer4")
	f.SetStderr(testutil.LogWriter(t))
	f.EnableProject("rsc/tmp")
	f.ReplaceText("zyzzyva", "ZYZZYVA")
	f.EnableEdits()
	f.SetTimeLimit(time.Time{})
	f.Run(ctx)
	// t.Logf("output:\n%s", buf)
	if bytes.Contains(buf.Bytes(), []byte("commentfix rewrite")) {
		t.Fatalf("logs incorrectly mention rewrite of comment:\n%s", buf.Bytes())
	}

	// Reverse the replacement and run again with same name; should not consider any comments.
	lg, buf = testutil.SlogBuffer()
	f = New(lg, gh, db, "fixer4")
	f.SetStderr(testutil.LogWriter(t))
	f.EnableProject("rsc/tmp")
	f.ReplaceText("c", "C")
	f.EnableEdits()
	f.SetTimeLimit(time.Time{})
	f.Run(ctx)
	// t.Logf("output:\n%s", buf)
	if bytes.Contains(buf.Bytes(), []byte("commentfix rewrite")) {
		t.Fatalf("logs incorrectly mention rewrite of comment:\n%s", buf.Bytes())
	}
}

func TestFixGitHubIssue(t *testing.T) {
	ctx := context.Background()
	t.Run("basic", func(t *testing.T) {
		f, project, buf, check := newFixer(t)
		check(f.FixGitHubIssue(ctx, project, 18))
		expect(t, buf, "commentfix rewrite", 2) // fix body and comment
	})

	t.Run("twice", func(t *testing.T) {
		f, project, buf, check := newFixer(t)
		check(f.FixGitHubIssue(ctx, project, 18))
		expect(t, buf, "commentfix rewrite", 2) // fix body and comment

		// Running Fix again doesn't change anything because fixes were
		// already applied.
		check(f.FixGitHubIssue(ctx, project, 18))
		expect(t, buf, "commentfix rewrite", 2) // nothing new
		expect(t, buf, "commentfix already applied", 2)
	})

	t.Run("fix-run", func(t *testing.T) {
		f, project, buf, check := newFixer(t)
		check(f.FixGitHubIssue(ctx, project, 20))
		expect(t, buf, "commentfix rewrite", 1) // fix body of issue 20

		// Run still fixes issue 18 because FixGitHubIssue
		// doesn't modify Run's watcher.
		check(f.Run(ctx))
		expect(t, buf, "commentfix rewrite", 3) // fix body and comment of issue 18
		expect(t, buf, "commentfix already applied", 1)
	})

	t.Run("fix-run-watcher", func(t *testing.T) {
		f, project, buf, check := newFixer(t)
		check(f.FixGitHubIssue(ctx, project, 18))
		check(f.FixGitHubIssue(ctx, project, 20))
		expect(t, buf, "commentfix rewrite", 3) // fix body of issue 20

		// Run sees that fixes have already been applied and advances
		// watcher.
		check(f.Run(ctx))
		expect(t, buf, "commentfix rewrite", 3) // no change
		expect(t, buf, "commentfix already applied", 3)

		// Run doesn't do anything because its watcher has been advanced.
		check(f.Run(ctx))
		expect(t, buf, "commentfix rewrite", 3)         // no change
		expect(t, buf, "commentfix already applied", 3) // no change
	})

	t.Run("fix-run-concurrent", func(t *testing.T) {
		f, project, buf, check := newFixer(t)

		var wg sync.WaitGroup

		wg.Add(1)
		go func() {
			check(f.FixGitHubIssue(ctx, project, 20))
			wg.Done()
		}()

		wg.Add(1)
		go func() {
			check(f.Run(ctx))
			wg.Done()
		}()

		wg.Add(1)
		go func() {
			check(f.FixGitHubIssue(ctx, project, 18))
			wg.Done()
		}()

		wg.Wait()

		// Each action is attempted twice.
		expect(t, buf, "commentfix rewrite", 3)
		expect(t, buf, "commentfix already applied", 3)
	})

	t.Run("fix-concurrent", func(t *testing.T) {
		f, project, buf, check := newFixer(t)

		var wg sync.WaitGroup

		n := 5
		wg.Add(n)
		for range n {
			go func() {
				check(f.FixGitHubIssue(ctx, project, 20))
				wg.Done()
			}()
		}

		wg.Wait()

		expect(t, buf, "commentfix rewrite", 1)
		expect(t, buf, "commentfix already applied", n-1)
	})
}

func expect(t *testing.T, buf *bytes.Buffer, action string, n int) {
	t.Helper()

	if mentions := bytes.Count(buf.Bytes(), []byte(action)); mentions != n {
		t.Errorf("logs mention %q %d times, want %d mentions:\n%s", action, mentions, n, buf.Bytes())
	}
}

func newFixer(t *testing.T) (_ *Fixer, project string, _ *bytes.Buffer, check func(error)) {
	gh := testGitHub(t)
	db := storage.MemDB()
	lg, buf := testutil.SlogBuffer()
	f := New(lg, gh, db, t.Name())
	f.SetStderr(testutil.LogWriter(t))
	project = "rsc/tmp"
	f.EnableProject(project)
	f.ReplaceText("cancelled", "canceled")
	f.SetTimeLimit(time.Time{})
	f.EnableEdits()
	return f, project, buf, testutil.Checker(t)
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

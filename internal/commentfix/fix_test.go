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

	testutil.StopPanic(func() {
		var f Fixer
		f.Run(ctx)
		t.Errorf("Run on zero Fixer did not panic")
	})
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
	testGH := func() *github.Client {
		db := storage.MemDB()
		gh := github.New(testutil.Slogger(t), db, nil, nil)
		gh.Testing().AddIssue("rsc/tmp", &github.Issue{
			Number:    18,
			Title:     "spellchecking",
			Body:      "Contexts are cancelled.",
			CreatedAt: "2024-06-17T20:16:49-04:00",
			UpdatedAt: "2024-06-17T20:16:49-04:00",
		})
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

		return gh
	}

	// Check for comment with too-new cutoff and edits disabled.
	// Finds nothing but also no-op.
	gh := testGH()
	lg, buf := testutil.SlogBuffer()
	f := New(lg, gh, "fixer1")
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
	f = New(lg, gh, "fixer1")
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
	f = New(lg, gh, "fixer2")
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
	f = New(lg, gh, "fixer2")
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
	f = New(lg, gh, "fixer3")
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
	f = New(lg, gh, "fixer4")
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
	f = New(lg, gh, "fixer4")
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

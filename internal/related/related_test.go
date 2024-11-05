// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package related

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/oscar/internal/actions"
	"golang.org/x/oscar/internal/diff"
	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/embeddocs"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/search"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

var ctx = context.Background()

func TestRun(t *testing.T) {
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	gh := github.New(lg, db, nil, nil)
	gh.Testing().LoadTxtar("../testdata/markdown.txt")
	gh.Testing().LoadTxtar("../testdata/rsctmp.txt")

	run := func(p *Poster) {
		t.Helper()
		check(p.Run(ctx))
		check(actions.Run(ctx, lg, db))
	}

	dc := docs.New(lg, db)
	docs.Sync(dc, gh)

	vdb := storage.MemVectorDB(db, lg, "vecs")
	embeddocs.Sync(ctx, lg, vdb, llm.QuoteEmbedder(), dc)

	vdb = storage.MemVectorDB(db, lg, "vecs")
	p := New(lg, db, gh, vdb, dc, "postname")
	p.EnableProject("rsc/markdown")
	p.SetTimeLimit(time.Time{})
	run(p)
	checkActionLog(t, db, nil)
	actions.ClearLogForTesting(t, db)

	p.EnablePosts()
	run(p)
	checkActionLog(t, db, map[int64]string{13: post13, 19: post19})
	actions.ClearLogForTesting(t, db)

	p.EnableProject("rsc/markdown")
	p.SetTimeLimit(time.Time{})
	p.EnablePosts()
	run(p)
	checkActionLog(t, db, nil)
	actions.ClearLogForTesting(t, db)

	for i := range 4 {
		p := New(lg, db, gh, vdb, dc, "postnameloop."+fmt.Sprint(i))
		p.EnableProject("rsc/markdown")
		p.SetTimeLimit(time.Time{})
		switch i {
		case 0:
			p.SkipTitlePrefix("feature: ")
		case 1:
			p.SkipTitleSuffix("for heading")
		case 2:
			p.SkipBodyContains("For example, this heading")
		case 3:
			p.SkipBodyContains("For example, this heading")
			p.SkipBodyContains("ZZZ")
		}
		p.EnablePosts()
		run(p)
		checkActionLog(t, db, map[int64]string{13: post13})
		actions.ClearLogForTesting(t, db)
	}

	p = New(lg, db, gh, vdb, dc, "postname2")
	p = New(lg, db, gh, vdb, dc, "postname3")
	p.EnableProject("rsc/markdown")
	p.SetMinScore(2.0) // impossible
	p.SetTimeLimit(time.Time{})
	p.EnablePosts()
	run(p)
	checkActionLog(t, db, nil)
	actions.ClearLogForTesting(t, db)

	p = New(lg, db, gh, vdb, dc, "postname4")
	p.EnableProject("rsc/markdown")
	p.SetMinScore(2.0) // impossible
	p.SetTimeLimit(time.Date(2222, 1, 1, 1, 1, 1, 1, time.UTC))
	p.EnablePosts()
	run(p)
	checkActionLog(t, db, nil)
	actions.ClearLogForTesting(t, db)

	p = New(lg, db, gh, vdb, dc, "postname5")
	p.EnableProject("rsc/markdown")
	p.SetMinScore(0)   // everything
	p.SetMaxResults(0) // except none
	p.SetTimeLimit(time.Time{})
	p.EnablePosts()
	run(p)
	checkActionLog(t, db, nil)
	actions.ClearLogForTesting(t, db)
}

func TestPost(t *testing.T) {
	check := testutil.Checker(t)

	post := func(p *Poster, project string, issues ...int64) {
		t.Helper()
		for _, iss := range issues {
			check(p.Post(ctx, project, iss))
		}
		check(actions.Run(ctx, p.slog, p.db))
	}

	run := func(p *Poster) {
		check(p.Run(ctx))
		check(actions.Run(ctx, p.slog, p.db))
	}

	t.Run("basic", func(t *testing.T) {
		p, _, project, _ := newTestPoster(t)

		post(p, project, 19, 13)
		checkActionLog(t, p.db, map[int64]string{13: post13, 19: post19})
	})

	t.Run("double-post", func(t *testing.T) {
		p, _, project, _ := newTestPoster(t)
		post(p, project, 13, 13)
		checkActionLog(t, p.db, map[int64]string{13: post13})
	})

	t.Run("post-run", func(t *testing.T) {
		p, buf, project, _ := newTestPoster(t)

		post(p, project, 19)
		latestDone := checkActionLog(t, p.db, map[int64]string{19: post19})
		testutil.ExpectLog(t, buf, "advanced watcher", 0)

		// Post does not advance Run's watcher, so it operates on all unhandled issues.
		run(p)
		latestDone = checkActionLogAfter(t, p.db, map[int64]string{13: post13}, latestDone)
		testutil.ExpectLog(t, buf, "advanced watcher", 2) // issue 13 and 19 both advance watcher

		// Run is a no-op because previous call to run advanced watcher past issue 19.
		run(p)
		checkActionLogAfter(t, p.db, nil, latestDone)
		testutil.ExpectLog(t, buf, "advanced watcher", 2) // no change
	})

	t.Run("post-run-async", func(t *testing.T) {
		p, _, project, _ := newTestPoster(t)

		// OK to run Post in the middle of a Run.
		done := make(chan struct{})
		go func() {
			run(p)
			done <- struct{}{}
		}()
		post(p, project, 19)
		<-done
		checkActionLog(t, p.db, map[int64]string{13: post13, 19: post19})
	})
}

func TestPostError(t *testing.T) {
	t.Run("event not in DB", func(t *testing.T) {
		p, _, project, _ := newTestPoster(t)

		wantErr := errEventNotFound
		// issue 42 is not in the project
		if err := p.Post(ctx, project, 42); !errors.Is(err, wantErr) {
			t.Fatalf("Post err = %v, want %v", err, wantErr)
		}
	})

	t.Run("issue not in Vector DB", func(t *testing.T) {
		p, _, project, _ := newTestPoster(t)

		// Vector search will fail if there is no embedding
		// for the issue.
		id := int64(19)
		p.vdb.Delete(issueURL(project, id))

		wantErr := errVectorSearchFailed
		if err := p.Post(ctx, project, id); !errors.Is(err, wantErr) {
			t.Fatalf("Post err = %v, want %v", err, wantErr)
		}
	})
}

func TestPostComment(t *testing.T) {
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	gh := github.New(lg, db, nil, nil)
	p := New(lg, db, gh, nil, nil, t.Name())

	results := []search.Result{
		{
			Kind:         search.KindGitHubIssue,
			VectorResult: storage.VectorResult{ID: "https://github.com/rsc/markdown/issues/1"},
			Title:        "Support Github Emojis",
		},
		{
			Kind:         search.KindGitHubDiscussion,
			VectorResult: storage.VectorResult{ID: "https://github.com/golang/go/discussions/67901"},
			Title:        "gabyhelp feedback",
		},
		{
			Kind:         search.KindGoBlog,
			VectorResult: storage.VectorResult{ID: "https://go.dev/blog/govulncheck"},
			Title:        "Govulncheck v1.0.0 is released!",
		},
		{
			Kind:         search.KindGoGerritChange,
			VectorResult: storage.VectorResult{ID: "https://go-review.googlesource.com/c/test/+/1#related-content"},
			Title:        "all: update dependencies",
		},
		{
			Kind:         search.KindGoogleGroupConversation,
			VectorResult: storage.VectorResult{ID: "https://groups.google.com/g/golang-nuts/c/MKgGqer_taI"},
			Title:        "Returning a pointer or value struct.",
		},
		{
			Kind:         search.KindGitHubIssue,
			VectorResult: storage.VectorResult{ID: "https://github.com/rsc/markdown/issues/2"},
			Title:        "allow capital X in task list items",
		},
		{
			Kind:         search.KindGoWiki,
			VectorResult: storage.VectorResult{ID: "https://go.dev/wiki/Iota"},
			Title:        "Go Wiki: Iota",
		},
		{
			Kind:         search.KindGoReference,
			VectorResult: storage.VectorResult{ID: "https://go.dev/ref/spec"},
			Title:        "The Go Programming Language Specification",
		},
	}

	want := `**Related Issues**

 - [Support Github Emojis](https://github.com/rsc/markdown/issues/1) <!-- score=0.00000 -->
 - [allow capital X in task list items](https://github.com/rsc/markdown/issues/2) <!-- score=0.00000 -->

**Related Code Changes**

 - [all: update dependencies](https://go-review.googlesource.com/c/test/+/1#related-content) <!-- score=0.00000 -->

**Related Documentation**

 - [Govulncheck v1.0.0 is released!](https://go.dev/blog/govulncheck) <!-- score=0.00000 -->
 - [Go Wiki: Iota](https://go.dev/wiki/Iota) <!-- score=0.00000 -->
 - [The Go Programming Language Specification](https://go.dev/ref/spec) <!-- score=0.00000 -->

**Related Discussions**

 - [gabyhelp feedback](https://github.com/golang/go/discussions/67901) <!-- score=0.00000 -->
 - [Returning a pointer or value struct.](https://groups.google.com/g/golang-nuts/c/MKgGqer_taI) <!-- score=0.00000 -->

<sub>(Emoji vote if this was helpful or unhelpful; more detailed feedback welcome in [this discussion](https://github.com/golang/go/discussions/67901).)</sub>
`

	if got := p.comment(results); want != got {
		t.Errorf("want %s comment; got %s", want, got)
	}
}

func newTestPoster(t *testing.T) (_ *Poster, out *bytes.Buffer, project string, check func(err error)) {
	t.Helper()

	lg, out := testutil.SlogBuffer()
	db := storage.MemDB()
	gh := github.New(lg, db, nil, nil)
	gh.Testing().LoadTxtar("../testdata/markdown.txt")
	gh.Testing().LoadTxtar("../testdata/rsctmp.txt")

	dc := docs.New(lg, db)
	docs.Sync(dc, gh)

	vdb := storage.MemVectorDB(db, lg, "vecs")
	embeddocs.Sync(ctx, lg, vdb, llm.QuoteEmbedder(), dc)

	p := New(lg, db, gh, vdb, dc, t.Name())
	project = "rsc/markdown"
	p.EnableProject(project)
	p.SetTimeLimit(time.Time{})
	p.EnablePosts()

	return p, out, project, testutil.Checker(t)
}

// checkActionLog calls checkActionLogAfter with the zero time.
func checkActionLog(t *testing.T, db storage.DB, want map[int64]string) time.Time {
	return checkActionLogAfter(t, db, want, time.Time{})
}

// checkActionLogAfter compares the contents of the action log after start with the values in want.
// The actions in the log must all be of type [action].
// Each key in want is the issue number of a completed action, and each value must match the action's
// comment body.
// checkActionLogAfter returns the done time of the latest matched action.
func checkActionLogAfter(t *testing.T, db storage.DB, want map[int64]string, start time.Time) time.Time {
	t.Helper()
	entries := slices.Collect(actions.ScanAfter(testutil.Slogger(t), db, start, nil))
	for _, e := range entries {
		if !e.IsDone() {
			continue
		}
		var a action
		if err := json.Unmarshal(e.Action, &a); err != nil {
			t.Fatal(err)
		}
		if a.Issue.Project() != "rsc/markdown" {
			t.Errorf("posted to unexpected project: %v", e)
			continue
		}
		w, ok := want[a.Issue.Number]
		if !ok {
			t.Errorf("post to unexpected issue: %v", e)
			continue
		}
		delete(want, a.Issue.Number)
		if strings.TrimSpace(a.Changes.Body) != strings.TrimSpace(w) {
			t.Errorf("rsc/markdown#%d: wrong post:\n%s", a.Issue.Number,
				string(diff.Diff("want", []byte(w), "have", []byte(a.Changes.Body))))
		}
	}
	for _, issue := range slices.Sorted(maps.Keys(want)) {
		t.Errorf("did not see post on rsc/markdown#%d", issue)
	}
	if t.Failed() {
		t.FailNow()
	}
	if len(entries) > 0 {
		return entries[len(entries)-1].Done
	}
	return time.Time{}
}

var post13 = unQUOT(`**Related Issues**

 - [goldmark and markdown diff with h1 inside p #6 (closed)](https://github.com/rsc/markdown/issues/6) <!-- score=0.92657 -->
 - [Support escaped \QUOT|\QUOT in table cells #9 (closed)](https://github.com/rsc/markdown/issues/9) <!-- score=0.91858 -->
 - [markdown: fix markdown printing for inline code #12 (closed)](https://github.com/rsc/markdown/issues/12) <!-- score=0.91325 -->
 - [markdown: emit Info in CodeBlock markdown #18 (closed)](https://github.com/rsc/markdown/issues/18) <!-- score=0.91129 -->
 - [feature: synthesize lowercase anchors for heading #19](https://github.com/rsc/markdown/issues/19) <!-- score=0.90867 -->
 - [Replace newlines with spaces in alt text #4 (closed)](https://github.com/rsc/markdown/issues/4) <!-- score=0.90859 -->
 - [allow capital X in task list items #2 (closed)](https://github.com/rsc/markdown/issues/2) <!-- score=0.90850 -->
 - [build(deps): bump golang.org/x/text from 0.3.6 to 0.3.8 in /rmplay #10](https://github.com/rsc/tmp/issues/10) <!-- score=0.90453 -->
 - [Render reference links in Markdown #14 (closed)](https://github.com/rsc/markdown/issues/14) <!-- score=0.90175 -->
 - [Render reference links in Markdown #15 (closed)](https://github.com/rsc/markdown/issues/15) <!-- score=0.90103 -->

<sub>(Emoji vote if this was helpful or unhelpful; more detailed feedback welcome in [this discussion](https://github.com/golang/go/discussions/67901).)</sub>
`)

var post19 = unQUOT(`**Related Issues**

 - [allow capital X in task list items #2 (closed)](https://github.com/rsc/markdown/issues/2) <!-- score=0.92943 -->
 - [Support escaped \QUOT|\QUOT in table cells #9 (closed)](https://github.com/rsc/markdown/issues/9) <!-- score=0.91994 -->
 - [goldmark and markdown diff with h1 inside p #6 (closed)](https://github.com/rsc/markdown/issues/6) <!-- score=0.91813 -->
 - [Render reference links in Markdown #14 (closed)](https://github.com/rsc/markdown/issues/14) <!-- score=0.91513 -->
 - [Render reference links in Markdown #15 (closed)](https://github.com/rsc/markdown/issues/15) <!-- score=0.91487 -->
 - [Empty column heading not recognized in table #7 (closed)](https://github.com/rsc/markdown/issues/7) <!-- score=0.90874 -->
 - [Correctly render reference links in Markdown #13](https://github.com/rsc/markdown/issues/13) <!-- score=0.90867 -->
 - [markdown: fix markdown printing for inline code #12 (closed)](https://github.com/rsc/markdown/issues/12) <!-- score=0.90795 -->
 - [Replace newlines with spaces in alt text #4 (closed)](https://github.com/rsc/markdown/issues/4) <!-- score=0.90278 -->
 - [build(deps): bump golang.org/x/text from 0.3.6 to 0.3.8 in /rmplay #10](https://github.com/rsc/tmp/issues/10) <!-- score=0.90259 -->

<sub>(Emoji vote if this was helpful or unhelpful; more detailed feedback welcome in [this discussion](https://github.com/golang/go/discussions/67901).)</sub>
`)

func unQUOT(s string) string { return strings.ReplaceAll(s, "QUOT", "`") }

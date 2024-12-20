// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package overview

import (
	"context"
	"testing"
	"time"

	"golang.org/x/oscar/internal/actions"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/llmapp"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

// This test checks that the timing logic of [Client.Run] works -
// the real internals are tested in [poster.run] and [Client.ForIssue].
func TestClientRun(t *testing.T) {
	lg := testutil.Slogger(t)
	db := storage.MemDB()
	lc := llmapp.New(lg, llm.EchoContentGenerator(), db)
	check := testutil.Checker(t)

	gh := github.New(lg, db, nil, nil)
	project := "test/test"
	gh.Testing().AddIssue(project, &github.Issue{Number: 1, CreatedAt: jan1_2024})
	gh.Testing().AddIssueComment(project, 1, &github.IssueComment{Body: "hello"})

	c := New(lg, db, gh, lc, "test", "testbot")
	c.EnableProject(project)
	c.SetMinComments(1)
	c.AutoApprove()

	ctx := context.Background()

	lastRun := time.Date(2024, 12, 2, 0, 0, 0, 0, time.UTC)
	check(c.run(ctx, lastRun))
	check(actions.Run(ctx, lg, db))
	edits := gh.Testing().Edits()
	if len(edits) == 0 {
		t.Fatal("Client.run (first): expected edits, got none")
	}
	gh.Testing().ClearEdits()

	gh.Testing().AddIssue(project, &github.Issue{Number: 2, CreatedAt: jan1_2024})
	gh.Testing().AddIssueComment(project, 2, &github.IssueComment{Body: "hello"})

	// Not enough time has passed - skip the run.
	check(c.run(ctx, lastRun.Add(time.Hour)))
	check(actions.Run(ctx, lg, db))
	if l := len(gh.Testing().Edits()); l != 0 {
		t.Fatalf("Client.run (second): expected no edits, got %d", l)
	}

	// Enough time has passed - do the run.
	check(c.run(ctx, lastRun.Add(minTimeBetweenUpdates+time.Microsecond)))
	check(actions.Run(ctx, lg, db))
	if len(gh.Testing().Edits()) == 0 {
		t.Fatalf("Client.run (third): expected edits, got none")
	}
}

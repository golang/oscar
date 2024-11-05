// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

func TestIssueRules(t *testing.T) {
	ctx := context.Background()
	db := storage.MemDB()
	llm := &ruleTestGenerator{}
	project := "project1"
	eventKind := "github.Event"
	api := "/issues"
	issueID = 999

	// Put an issue into the database.
	// TODO: this is kind of hacky.
	i := new(Issue)
	i.Number = issueID
	i.User = User{Login: "user"}
	i.Title = "title"
	i.Body = "body"
	key := ordered.Encode(project, issueID, api, issueID)
	val := ordered.Encode(ordered.Raw(storage.JSON(i)))
	batch := db.Batch()
	timed.Set(db, batch, eventKind, key, val)
	batch.Apply()

	// Ask about rule violations.
	r, err := IssueRules(ctx, llm, db, project, issueID)
	if err != nil {
		t.Fatalf("IssueRules failed with %v", err)
	}

	// Check result.
	if r.Issue.Number != issueID {
		t.Errorf("issue ID did not round trip correctly, got %d want %d", r.Issue.Number, issueID)
	}
	if !strings.Contains(r.Response, "\n- The issue title must start") {
		t.Errorf("expected the issue title rule failed, but it didn't. Total output: %s", r.Response)
	}
	if n := strings.Count(r.Response, "\n- "); n != 1 {
		t.Errorf("wanted 1 rule violation, got %d", n)
	}
}

// Implements llm.TextGenerator
type ruleTestGenerator struct {
}

func (g *ruleTestGenerator) Model() string { return "ruleTestGenerator" }
func (g *ruleTestGenerator) GenerateText(ctx context.Context, promptParts ...string) (string, error) {
	req := strings.Join(promptParts, " ")
	if strings.Contains(req, "Your job is to categorize") {
		// categorize request. Always report it as a "bug".
		return "bug\nI think this is a bug.", nil
	}
	if strings.Contains(req, "Your job is to decide") {
		// rule request. Report that the title rule failed and the others succeeded.
		if strings.Contains(req, "The issue title must start") {
			return "no\nThe title is malformed.", nil
		}
		return "yes\nThe rule is obeyed.", nil

	}
	return "UNK", nil
}

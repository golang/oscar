// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oscar/internal/storage"
	"golang.org/x/tools/txtar"
)

// Testing returns a TestingClient, which provides access to Client functionality
// intended for testing.
// Testing only returns a non-nil TestingClient in testing mode,
// which is active if the current program is a test binary (that is, [testing.Testing] returns true)
// or if [Client.EnableTesting] has been called.
// Otherwise, Testing returns nil.
//
// Each Client has only one TestingClient associated with it. Every call to Testing returns the same TestingClient.
func (c *Client) Testing() *TestingClient {
	if !testing.Testing() && !c.testing {
		return nil
	}

	c.testMu.Lock()
	defer c.testMu.Unlock()
	if c.testClient == nil {
		c.testClient = &TestingClient{c: c}
	}
	return c.testClient
}

// EnableTesting enables testing mode, in which edits are diverted and a TestingClient is available.
// If the program is itself a test binary (built or run using “go test”), testing mode is enabled automatically.
// EnableTesting can be useful in experimental programs to make sure that no edits
// are applied to GitHub.
func (c *Client) EnableTesting() {
	c.testing = true
}

// A TestingEdit is a diverted edit, which was logged instead of actually applied on GitHub.
type TestingEdit struct {
	Project             string
	Issue               int64
	Comment             int64
	IssueChanges        *IssueChanges
	IssueCommentChanges *IssueCommentChanges
}

// String returns a basic string representation of the edit.
func (e *TestingEdit) String() string {
	switch {
	case e.IssueChanges != nil:
		js, _ := json.Marshal(e.IssueChanges)
		if e.Issue == 0 {
			return fmt.Sprintf("PostIssue(%s, %s)", e.Project, js)
		}
		return fmt.Sprintf("EditIssue(%s#%d, %s)", e.Project, e.Issue, js)

	case e.IssueCommentChanges != nil:
		js, _ := json.Marshal(e.IssueCommentChanges)
		if e.Comment == 0 {
			return fmt.Sprintf("PostIssueComment(%s#%d, %s)", e.Project, e.Issue, js)
		}
		return fmt.Sprintf("EditIssueComment(%s#%d.%d, %s)", e.Project, e.Issue, e.Comment, js)
	}
	return "?"
}

// A TestingClient provides access to Client functionality intended for testing.
//
// See [Client.Testing] for a description of testing mode.
type TestingClient struct {
	c *Client
}

// addEvent adds an event to the Client's underlying database.
func (tc *TestingClient) addEvent(url string, e *Event) {
	js := json.RawMessage(storage.JSON(e.Typed))

	tc.c.testMu.Lock()
	if tc.c.testEvents == nil {
		tc.c.testEvents = make(map[string]json.RawMessage)
	}
	tc.c.testEvents[url] = js
	tc.c.testMu.Unlock()

	b := tc.c.db.Batch()
	tc.c.writeEvent(b, e.Project, e.Issue, e.API, e.ID, js)
	b.Apply()
}

var issueID int64 = 1e9

// AddIssue adds the given issue to the identified project,
// assigning it a new issue number starting at 10⁹.
// AddIssue creates a new entry in the associated [Client]'s
// underlying database, so other Client's using the same database
// will see the issue too.
//
// NOTE: Only one TestingClient should be adding issues,
// since they do not coordinate in the database about ID assignment.
// Perhaps they should, but normally there is just one Client.
func (tc *TestingClient) AddIssue(project string, issue *Issue) {
	id := atomic.AddInt64(&issueID, +1)
	issue.URL = fmt.Sprintf("https://api.github.com/repos/%s/issues/%d", project, issue.Number)
	issue.HTMLURL = fmt.Sprintf("https://github.com/%s/issues/%d", project, issue.Number)
	tc.addEvent(issue.URL, &Event{
		Project: project,
		Issue:   issue.Number,
		API:     "/issues",
		ID:      id,
		Typed:   issue,
	})
}

var commentID int64 = 1e10

// AddIssueComment adds the given issue comment to the identified project issue,
// assigning it a new comment ID starting at 10¹⁰.
// AddIssueComment creates a new entry in the associated [Client]'s
// underlying database, so other Client's using the same database
// will see the issue comment too.
//
// NOTE: Only one TestingClient should be adding issues,
// since they do not coordinate in the database about ID assignment.
// Perhaps they should, but normally there is just one Client.
func (tc *TestingClient) AddIssueComment(project string, issue int64, comment *IssueComment) {
	id := atomic.AddInt64(&commentID, +1)
	comment.URL = fmt.Sprintf("https://api.github.com/repos/%s/issues/comments/%d", project, id)
	comment.HTMLURL = fmt.Sprintf("https://github.com/%s/issues/%d#issuecomment-%d", project, issue, id)
	tc.addEvent(comment.URL, &Event{
		Project: project,
		Issue:   issue,
		API:     "/issues/comments",
		ID:      id,
		Typed:   comment,
	})
}

var eventID int64 = 1e11

// AddIssueEvent adds the given issue event to the identified project issue,
// assigning it a new comment ID starting at 10¹¹.
// AddIssueEvent creates a new entry in the associated [Client]'s
// underlying database, so other Client's using the same database
// will see the issue event too.
//
// NOTE: Only one TestingClient should be adding issues,
// since they do not coordinate in the database about ID assignment.
// Perhaps they should, but normally there is just one Client.
func (tc *TestingClient) AddIssueEvent(project string, issue int64, event *IssueEvent) {
	id := atomic.AddInt64(&eventID, +1)
	event.ID = id
	event.URL = fmt.Sprintf("https://api.github.com/repos/%s/issues/events/%d", project, id)
	tc.addEvent(event.URL, &Event{
		Project: project,
		Issue:   issue,
		API:     "/issues/events",
		ID:      id,
		Typed:   event,
	})
}

// Edits returns a list of all the edits that have been applied using [Client] methods
// (for example [Client.EditIssue], [Client.EditIssueComment], [Client.PostIssueComment]).
// These edits have not been applied on GitHub, only diverted into the [TestingClient].
//
// See [Client.Testing] for a description of testing mode.
//
// NOTE: These edits are not applied to the underlying database,
// since they are also not applied to the underlying database when
// using a real connection to GitHub; instead we wait for the next
// sync to download GitHub's view of the edits.
// See [Client.EditIssue].
func (tc *TestingClient) Edits() []*TestingEdit {
	tc.c.testMu.Lock()
	defer tc.c.testMu.Unlock()

	return tc.c.testEdits
}

// ClearEdits clears the list of edits that are meant to be applied
func (tc *TestingClient) ClearEdits() {
	tc.c.testMu.Lock()
	defer tc.c.testMu.Unlock()

	tc.c.testEdits = nil
}

// divertEdits reports whether edits are being diverted.
func (c *Client) divertEdits() bool {
	return c.testing
}

// LoadTxtar loads issue histories from the named txtar file,
// writing them to the database using [TestingClient.AddIssue],
// [TestingClient.AddIssueComment], and [TestingClient.AddIssueEvent].
//
// The file should contain a txtar archive (see [golang.org/x/tools/txtar]).
// Each file in the archive should be named “project#n” (for example “golang/go#123”)
// and contain an issue history in the format printed by the [rsc.io/github/issue] command.
// See the file ../testdata/rsctmp.txt for an example.
//
// To download a specific set of issues into a new file, you can use a script like:
//
//	go install rsc.io/github/issue@latest
//	project=golang/go
//	(for i in 1 2 3 4 5
//	do
//		echo "-- $project#$i --"
//		issue -p $project $i
//	done) > testdata/proj.txt
func (tc *TestingClient) LoadTxtar(file string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	err = tc.LoadTxtarData(data)
	if err != nil {
		err = &os.PathError{Op: "load", Path: file, Err: err}
	}
	return err
}

// LoadTxtarData loads issue histories from the txtar file content data.
// See [LoadTxtar] for a description of the format.
func (tc *TestingClient) LoadTxtarData(data []byte) error {
	ar := txtar.Parse(data)
	for _, file := range ar.Files {
		project, num, ok := strings.Cut(file.Name, "#")
		n, err := strconv.ParseInt(num, 10, 64)
		if !ok || strings.Count(project, "/") != 1 || err != nil || n <= 0 {
			return fmt.Errorf("invalid issue name %q (want 'org/repo#num')", file.Name)
		}

		data := string(file.Data)
		issue := &Issue{Number: n}

		cutTime := func(line string) (prefix string, tm string, ok bool) {
			if !strings.HasSuffix(line, ")") {
				return
			}
			i := strings.LastIndex(line, " (")
			if i < 0 {
				return
			}
			prefix, ts := strings.TrimSpace(line[:i]), line[i+2:len(line)-1]
			t, err := time.Parse("2006-01-02 15:04:05", ts)
			return prefix, t.UTC().Format(time.RFC3339), err == nil
		}

		// Read header
		for {
			line, rest, _ := strings.Cut(data, "\n")
			data = rest
			if line == "" {
				break
			}
			key, val, ok := strings.Cut(line, ":")
			if !ok {
				return fmt.Errorf("%s: invalid header line: %q", file.Name, line)
			}
			val = strings.TrimSpace(val)
			if val == "" {
				continue
			}
			switch key {
			case "Title":
				issue.Title = val
			case "State":
				issue.State = val
			case "Assignee":
				issue.Assignees = []User{{Login: val}}
			case "Closed":
				_, tm, ok := cutTime(" (" + val + ")")
				if !ok {
					return fmt.Errorf("%s: invalid close time: %q", file.Name, line)
				}
				issue.ClosedAt = tm
			case "Labels":
				if val != "" {
					for _, name := range strings.Split(val, ", ") {
						issue.Labels = append(issue.Labels, Label{Name: name})
					}
				}
			case "Milestone":
				issue.Milestone.Title = val
			case "URL":
				want := fmt.Sprintf("https://github.com/%s/issues/%d", project, issue.Number)
				pr := fmt.Sprintf("https://github.com/%s/pull/%d", project, issue.Number)
				if val == pr {
					issue.PullRequest = new(struct{})
					continue
				}
				if val != want {
					return fmt.Errorf("%s: invalid URL: %q, want %q", file.Name, val, want)
				}
			case "PR":
				issue.PullRequest = new(struct{})
			}
		}

		// Read updates.

		readBody := func() string {
			data = strings.TrimLeft(data, "\n")
			var text []string
			for len(data) > 0 && (data[0] == '\n' || data[0] == '\t') {
				s, rest, _ := strings.Cut(data, "\n")
				data = rest
				text = append(text, strings.TrimPrefix(s, "\t"))
			}
			if len(text) > 0 && text[len(text)-1] != "" {
				text = append(text, "")
			}
			return strings.Join(text, "\n")
		}

		haveReport := false
		for data != "" {
			line, rest, _ := strings.Cut(data, "\n")
			data = rest
			if line == "" {
				continue
			}
			prefix, tm, ok := cutTime(line)
			if !ok {
				return fmt.Errorf("%s: invalid event time: %q", file.Name, line)
			}
			line = prefix
			if who, ok := strings.CutPrefix(line, "Reported by "); ok {
				if haveReport {
					return fmt.Errorf("%s: multiple 'Reported by'", file.Name)
				}
				issue.Body = readBody()
				issue.CreatedAt = tm
				issue.UpdatedAt = tm
				issue.User = User{Login: who}
				haveReport = true
				tc.AddIssue(project, issue)
				continue
			}
			if who, ok := strings.CutPrefix(line, "Comment by "); ok {
				if !haveReport {
					return fmt.Errorf("%s: missing 'Reported by'", file.Name)
				}
				body := readBody()
				tc.AddIssueComment(project, issue.Number, &IssueComment{
					User:      User{Login: who},
					Body:      body,
					CreatedAt: tm,
					UpdatedAt: tm,
				})
				continue
			}
			op, ok := strings.CutPrefix(line, "* ")
			if !ok {
				return fmt.Errorf("%s: invalid event description: %q", file.Name, line)
			}
			if who, whom, ok := strings.Cut(op, " assigned "); ok {
				tc.AddIssueEvent(project, issue.Number, &IssueEvent{
					Actor:     User{Login: who},
					Event:     "assigned",
					CreatedAt: tm,
					Assignees: []User{{Login: whom}},
				})
				continue
			}
			if who, whom, ok := strings.Cut(op, " unassigned "); ok {
				tc.AddIssueEvent(project, issue.Number, &IssueEvent{
					Actor:     User{Login: who},
					Event:     "assigned",
					CreatedAt: tm,
					Assignees: []User{{Login: whom}},
				})
				continue
			}
			if who, label, ok := strings.Cut(op, " labeled "); ok {
				tc.AddIssueEvent(project, issue.Number, &IssueEvent{
					Actor:     User{Login: who},
					Event:     "labeled",
					CreatedAt: tm,
					Labels:    []Label{{Name: label}},
				})
				continue
			}
			if who, label, ok := strings.Cut(op, " unlabeled "); ok {
				tc.AddIssueEvent(project, issue.Number, &IssueEvent{
					Actor:     User{Login: who},
					Event:     "unlabeled",
					CreatedAt: tm,
					Labels:    []Label{{Name: label}},
				})
				continue
			}
			if who, title, ok := strings.Cut(op, " added to milestone "); ok {
				tc.AddIssueEvent(project, issue.Number, &IssueEvent{
					Actor:     User{Login: who},
					Event:     "milestoned",
					CreatedAt: tm,
					Milestone: Milestone{Title: title},
				})
				continue
			}
			if who, title, ok := strings.Cut(op, " removed from milestone "); ok {
				tc.AddIssueEvent(project, issue.Number, &IssueEvent{
					Actor:     User{Login: who},
					Event:     "demilestoned",
					CreatedAt: tm,
					Milestone: Milestone{Title: title},
				})
				continue
			}
			if who, ok := strings.CutSuffix(op, " changed title"); ok {
				if !strings.HasPrefix(data, "  - ") {
					return fmt.Errorf("%s: missing old issue title: %q", file.Name, line)
				}
				old, rest, _ := strings.Cut(data[len("  - "):], "\n")
				if !strings.HasPrefix(rest, "  + ") {
					return fmt.Errorf("%s: missing new issue title: %q", file.Name, line)
				}
				new, rest, _ := strings.Cut(rest[len("  + "):], "\n")
				data = rest
				tc.AddIssueEvent(project, issue.Number, &IssueEvent{
					Actor:     User{Login: who},
					Event:     "renamed",
					CreatedAt: tm,
					Rename: Rename{
						From: old,
						To:   new,
					},
				})
				continue
			}
			if who, commit, ok := strings.Cut(op, " closed in commit "); ok {
				readBody()
				tc.AddIssueEvent(project, issue.Number, &IssueEvent{
					Actor:     User{Login: who},
					Event:     "closed",
					CreatedAt: tm,
					CommitID:  commit, // note: truncated
				})
				continue
			}
			if who, commit, ok := strings.Cut(op, " merged in commit "); ok {
				readBody()
				tc.AddIssueEvent(project, issue.Number, &IssueEvent{
					Actor:     User{Login: who},
					Event:     "merged",
					CreatedAt: tm,
					CommitID:  commit, // note: truncated
				})
				continue
			}
			if who, commit, ok := strings.Cut(op, " referenced in commit "); ok {
				readBody()
				tc.AddIssueEvent(project, issue.Number, &IssueEvent{
					Actor:     User{Login: who},
					Event:     "merged",
					CreatedAt: tm,
					CommitID:  commit, // note: truncated
				})
				continue
			}
			if who, ok := strings.CutSuffix(op, " review_requested"); ok {
				readBody()
				tc.AddIssueEvent(project, issue.Number, &IssueEvent{
					Actor:     User{Login: who},
					Event:     "review_requested",
					CreatedAt: tm,
				})
				continue
			}
			if who, ok := strings.CutSuffix(op, " head_ref_force_pushed"); ok {
				readBody()
				tc.AddIssueEvent(project, issue.Number, &IssueEvent{
					Actor:     User{Login: who},
					Event:     "head_ref_force_pushed",
					CreatedAt: tm,
				})
				continue
			}
			if who, ok := strings.CutSuffix(op, " head_ref_deleted"); ok {
				readBody()
				tc.AddIssueEvent(project, issue.Number, &IssueEvent{
					Actor:     User{Login: who},
					Event:     "head_ref_deleted",
					CreatedAt: tm,
				})
				continue
			}
			if who, ok := strings.CutSuffix(op, " head_ref_restored"); ok {
				readBody()
				tc.AddIssueEvent(project, issue.Number, &IssueEvent{
					Actor:     User{Login: who},
					Event:     "head_ref_restored",
					CreatedAt: tm,
				})
				continue
			}
			if who, ok := strings.CutSuffix(op, " closed"); ok {
				tc.AddIssueEvent(project, issue.Number, &IssueEvent{
					Actor:     User{Login: who},
					Event:     "closed",
					CreatedAt: tm,
				})
				continue
			}
			return fmt.Errorf("%s: invalid event description: %q", file.Name, line)
		}
	}
	return nil
}

/* event types:
https://docs.github.com/en/rest/using-the-rest-api/issue-event-types?apiVersion=2022-11-28#issue-event-object-common-properties

added_to_project
assigned
automatic_base_change_failed
automatic_base_change_succeeded
base_ref_changed
closed
commented
committed
connected
convert_to_draft
converted_note_to_issue
converted_to_discussion
cross-referenced
demilestoned
deployed
deployment_environment_changed
disconnected
head_ref_deleted
head_ref_restored
head_ref_force_pushed
labeled
locked
mentioned
marked_as_duplicate
merged
milestoned
moved_columns_in_project
pinned
ready_for_review
referenced
removed_from_project
renamed
reopened
review_dismissed
review_requested
review_request_removed
reviewed
subscribed
transferred
unassigned
unlabeled
unlocked
unmarked_as_duplicate
unpinned
unsubscribed
user_blocked
*/

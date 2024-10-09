// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package discussion

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"golang.org/x/oscar/internal/storage"
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
	if !testing.Testing() {
		return nil
	}

	c.testMu.Lock()
	defer c.testMu.Unlock()
	if c.testClient == nil {
		c.testClient = &TestingClient{c: c}
	}
	return c.testClient
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
	e.JSON = js
	tc.c.testMu.Unlock()

	b := tc.c.db.Batch()
	tc.c.writeEvent(b, e)
	b.Apply()
}

var issueID int64 = 1e9

// AddDiscussion adds the given discussion to the identified project,
// assigning it a new issue number starting at 10⁹.
// AddDiscussion creates a new entry in the associated [Client]'s
// underlying database, so other Client's using the same database
// will see the issue too.
//
// NOTE: Only one TestingClient should be adding issues,
// since they do not coordinate in the database about ID assignment.
// Perhaps they should, but normally there is just one Client.
func (tc *TestingClient) AddDiscussion(project string, d *Discussion) int64 {
	id := atomic.AddInt64(&issueID, +1)
	d.URL = fmt.Sprintf("https://github.com/%s/discussions/%d", project, id)
	tc.addEvent(d.URL, &Event{
		Project:    project,
		Discussion: d.Number,
		API:        DiscussionAPI,
		ID:         id,
		Typed:      d,
	})
	return id
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
func (tc *TestingClient) AddComment(project string, disc int64, comment *Comment) int64 {
	id := atomic.AddInt64(&commentID, +1)
	comment.URL = fmt.Sprintf("https://github.com/%s/discussions/%d#discussioncomment-%d", project, disc, id)
	tc.addEvent(comment.URL, &Event{
		Project:    project,
		Discussion: disc,
		API:        CommentAPI,
		ID:         id,
		Typed:      comment,
	})
	return id
}

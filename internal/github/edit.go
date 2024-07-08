// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// NOTE: It's possible that we should elevate TestingEdit to a general
// “deferred edits” facility for use in looking at potential changes.
// On the other hand, higher-level code usually needs to know
// whether it's making changes or not, so that it can record that
// the work has been done, so normally “deferred edits” should be
// as high in the stack as possible, and the GitHub client is not.

// PostIssueComment posts a new comment with the given body (written in Markdown) on issue.
func (c *Client) PostIssueComment(issue *Issue, changes *IssueCommentChanges) error {
	if c.divertEdits() {
		c.testMu.Lock()
		defer c.testMu.Unlock()

		c.testEdits = append(c.testEdits, &TestingEdit{
			Project:             issue.Project(),
			Issue:               issue.Number,
			IssueCommentChanges: changes.clone(),
		})
		return nil
	}

	return c.post(issue.URL+"/comments", changes)
}

// DownloadIssue downloads the current issue JSON from the given URL
// and decodes it into an issue.
// Given an issue, c.DownloadIssue(issue.URL) fetches the very latest state for the issue.
func (c *Client) DownloadIssue(url string) (*Issue, error) {
	x := new(Issue)
	_, err := c.get(url, "", x)
	if err != nil {
		return nil, err
	}
	return x, nil
}

// DownloadIssueComment downloads the current comment JSON from the given URL
// and decodes it into an IssueComment.
// Given a comment, c.DownloadIssueComment(comment.URL) fetches the very latest state for the comment.
func (c *Client) DownloadIssueComment(url string) (*IssueComment, error) {
	x := new(IssueComment)
	_, err := c.get(url, "", x)
	if err != nil {
		return nil, err
	}
	return x, nil
}

type IssueCommentChanges struct {
	Body string `json:"body,omitempty"`
}

func (ch *IssueCommentChanges) clone() *IssueCommentChanges {
	x := *ch
	ch = &x
	return ch
}

// EditIssueComment changes the comment on GitHub to have the new body.
// It is typically a good idea to use c.DownloadIssueComment first and check
// that the live comment body matches the one obtained from the database,
// to minimize race windows.
func (c *Client) EditIssueComment(comment *IssueComment, changes *IssueCommentChanges) error {
	if c.divertEdits() {
		c.testMu.Lock()
		defer c.testMu.Unlock()

		c.testEdits = append(c.testEdits, &TestingEdit{
			Project:             comment.Project(),
			Issue:               comment.Issue(),
			Comment:             comment.CommentID(),
			IssueCommentChanges: changes.clone(),
		})
		return nil
	}

	return c.patch(comment.URL, changes)
}

// An IssueChanges specifies changes to make to an issue.
// Fields that are the empty string or a nil pointer are ignored.
//
// Note that Labels is the new set of all labels for the issue,
// not labels to add. If you are adding a single label,
// you need to include all the existing labels as well.
// Labels is a *[]string so that it can be set to new([]string)
// to clear the labels.
type IssueChanges struct {
	Title  string    `json:"title,omitempty"`
	Body   string    `json:"body,omitempty"`
	State  string    `json:"state,omitempty"`
	Labels *[]string `json:"labels,omitempty"`
}

func (ch *IssueChanges) clone() *IssueChanges {
	x := *ch
	ch = &x
	if ch.Labels != nil {
		x := slices.Clone(*ch.Labels)
		ch.Labels = &x
	}
	return ch
}

// EditIssue applies the changes to issue on GitHub.
func (c *Client) EditIssue(issue *Issue, changes *IssueChanges) error {
	if c.divertEdits() {
		c.testMu.Lock()
		defer c.testMu.Unlock()

		c.testEdits = append(c.testEdits, &TestingEdit{
			Project:      issue.Project(),
			Issue:        issue.Number,
			IssueChanges: changes.clone(),
		})
		return nil
	}

	return c.patch(issue.URL, changes)
}

// patch is like c.get but makes a PATCH request.
// Unlike c.get, it requires authentication.
func (c *Client) patch(url string, changes any) error {
	return c.json("PATCH", url, changes)
}

// post is like c.get but makes a POST request.
// Unlike c.get, it requires authentication.
func (c *Client) post(url string, body any) error {
	return c.json("POST", url, body)
}

// json is the general PATCH/POST implementation.
func (c *Client) json(method, url string, body any) error {
	js, err := json.Marshal(body)
	if err != nil {
		return err
	}

	auth, ok := c.secret.Get("api.github.com")
	if !ok && !testing.Testing() {
		return fmt.Errorf("no secret for api.github.com")
	}
	user, pass, _ := strings.Cut(auth, ":")

Redo:
	req, err := http.NewRequest(method, url, bytes.NewReader(js))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.SetBasicAuth(user, pass)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return fmt.Errorf("reading body: %v", err)
	}
	if c.rateLimit(resp) {
		goto Redo
	}
	if resp.StatusCode/10 != 20 { // allow 200, 201, maybe others
		return fmt.Errorf("%s\n%s", resp.Status, data)
	}
	return nil
}

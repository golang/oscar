// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
// It returns an API URL for the new comment, and a URL suitable for display.
func (c *Client) PostIssueComment(ctx context.Context, issue *Issue, changes *IssueCommentChanges) (id, url string, err error) {
	if c.divertEdits() {
		c.testMu.Lock()
		defer c.testMu.Unlock()

		c.testEdits = append(c.testEdits, &TestingEdit{
			Project:             issue.Project(),
			Issue:               issue.Number,
			IssueCommentChanges: changes.clone(),
		})
		return "test-api-url", "test-url", nil
	}

	body, err := c.post(ctx, issue.URL+"/comments", changes)
	if err != nil {
		return "", "", err
	}
	var res struct {
		APIURL     string `json:"url"`
		DisplayURL string `json:"html_url"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", "", err
	}
	return res.APIURL, res.DisplayURL, nil
}

// DownloadIssue downloads the current issue JSON from the given URL
// and decodes it into an issue.
// Given an issue, c.DownloadIssue(issue.URL) fetches the very latest state for the issue.
func (c *Client) DownloadIssue(ctx context.Context, url string) (*Issue, error) {
	x := new(Issue)
	_, err := c.get(ctx, url, "", x)
	if err != nil {
		return nil, err
	}
	return x, nil
}

// DownloadIssueComment downloads the current comment JSON from the given URL
// and decodes it into an IssueComment.
// Given a comment, c.DownloadIssueComment(comment.URL) fetches the very latest state for the comment.
func (c *Client) DownloadIssueComment(ctx context.Context, url string) (*IssueComment, error) {
	x := new(IssueComment)
	_, err := c.get(ctx, url, "", x)
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

func (ch *IssueCommentChanges) SetTitle(string) error {
	return errors.New("cannot set the title of an IssueComment")
}

func (ch *IssueCommentChanges) SetBody(s string) error {
	ch.Body = s
	return nil
}

// EditIssueComment changes the comment on GitHub to have the new body.
// It is typically a good idea to use c.DownloadIssueComment first and check
// that the live comment body matches the one obtained from the database,
// to minimize race windows.
func (c *Client) EditIssueComment(ctx context.Context, comment *IssueComment, changes *IssueCommentChanges) error {
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

	_, err := c.patch(ctx, comment.URL, changes)
	return err
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

func (ch *IssueChanges) SetTitle(s string) error {
	ch.Title = s
	return nil
}

func (ch *IssueChanges) SetBody(s string) error {
	ch.Body = s
	return nil
}

// EditIssue applies the changes to issue on GitHub.
func (c *Client) EditIssue(ctx context.Context, issue *Issue, changes *IssueChanges) error {
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

	_, err := c.patch(ctx, issue.URL, changes)
	return err
}

// patch is like c.get but makes a PATCH request.
// Unlike c.get, it requires authentication.
// It returns the response body on success.
func (c *Client) patch(ctx context.Context, url string, changes any) ([]byte, error) {
	return c.json(ctx, "PATCH", url, changes)
}

// post is like c.get but makes a POST request.
// Unlike c.get, it requires authentication.
// It returns the response body on success.
func (c *Client) post(ctx context.Context, url string, body any) ([]byte, error) {
	return c.json(ctx, "POST", url, body)
}

// json is the general PATCH/POST implementation.
// It returns the response body on success.
func (c *Client) json(ctx context.Context, method, url string, body any) ([]byte, error) {
	js, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	auth, ok := c.secret.Get("api.github.com")
	if !ok && !testing.Testing() {
		return nil, fmt.Errorf("no secret for api.github.com")
	}
	user, pass, _ := strings.Cut(auth, ":")

Redo:
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(js))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.SetBasicAuth(user, pass)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("reading body: %v", err)
	}
	if c.rateLimit(resp) {
		goto Redo
	}
	if resp.StatusCode/10 != 20 { // allow 200, 201, maybe others
		// Include body as part of error; don't return it separately.
		return nil, fmt.Errorf("%s\n%s", resp.Status, data)
	}
	return data, nil
}

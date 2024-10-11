// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gerrit

import (
	"fmt"
	"iter"
	"slices"
	"strings"

	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/storage/timed"
)

// LastWritten implements [docs.Entry.LastWritten].
func (ce *ChangeEvent) LastWritten() timed.DBTime {
	return ce.DBTime
}

// ToDocs converts a ChangeEvent to an embeddable document (wrapped
// as an iterator).
//
// This document consists of a change commit message and its comments.
// The ID for such documents is of the form
//
//	https://<gerrit-instance>/c/<repo>/+/<n>#related-content.
//
// The "#related-content" fragment is used to allow other types of
// gerrit documents to reuse the main portion of the change URL.
// The URL points to the top of the CL page since the fragment
// does not exist.
//
// ToDocs returns (nil, false) if any of the necessary data cannot be found
// in the client's db.
//
// Implements [docs.Source.ToDocs].
func (c *Client) ToDocs(ce *ChangeEvent) (iter.Seq[*docs.Doc], bool) {
	ch := c.change(ce)
	if ch == nil {
		c.slog.Error("gerrit.ChangeEvent.ToDocs cannot find change", "change", ce.ChangeNum)
		return nil, false
	}
	title := c.ChangeSubject(ch.ch)
	body, err := c.relatedDocBody(ch)
	if err != nil {
		c.slog.Error("gerrit.ChangeEvent.ToDocs cannot find comments", "change", ce.ChangeNum)
		return nil, false
	}
	if len(body) > geminiCharLimit {
		c.slog.Warn("gerrit.ChangeEvent.ToDocs potential truncation by gemini", "change", ce.ChangeNum, "docSize", len(body))
	}
	text := cleanBody(body)
	id := relatedDocURL(ch)
	return slices.Values([]*docs.Doc{{
		ID:    id,
		Title: title,
		Text:  text,
	}}), true
}

// geminiCharLimit is an approximate limit on the number of
// document characters a gemini text embedding can accept.
// Gemini text embedding models have an input token limit
// of 2048, where each token is about four characters long.
// Gemini truncates documents after this limit.
// For more info, see
// https://ai.google.dev/gemini-api/docs/models/gemini#text-embedding-and-embedding
const geminiCharLimit = 8200

// changeInfo accumulates information from [Change]
// and [ChangeEvent] needed to grab change subject,
// messages, and comments.
type changeInfo struct {
	instance string
	project  string
	number   int
	ch       *Change
}

// change returns a gerrit change information corresponding to ce.
// The project of the change must be one of projects.
func (c *Client) change(ce *ChangeEvent) *changeInfo {
	ci := &changeInfo{
		instance: ce.Instance,
		number:   ce.ChangeNum,
	}
	for p := range c.projects() {
		if ch := c.Change(p, ce.ChangeNum); ch != nil {
			ci.project = p // at most one project can match ce.ChangeNum
			ci.ch = ch
			return ci
		}
	}
	return nil
}

// relatedDocBody returns the document body for the gerrit change ci,
// intended for surfacing related content. The body consists of
// the most recent commit message followed by change messages and
// comments appearing in their chronological order. There is a new
// line added between each message and comment.
func (c *Client) relatedDocBody(ci *changeInfo) (string, error) {
	comments, err := c.comments(ci)
	if err != nil {
		return "", nil
	}
	messages := c.ChangeMessages(ci.ch)

	// Sort comments and messages based on their creation/update time.
	type datedMessage struct {
		date    TimeStamp
		message string
	}
	var dmsgs []datedMessage
	for _, cmt := range comments {
		dmsgs = append(dmsgs, datedMessage{date: cmt.Updated, message: cmt.Message})
	}
	for _, msg := range messages {
		dmsgs = append(dmsgs, datedMessage{date: msg.Date, message: msg.Message})
	}
	slices.SortStableFunc(dmsgs, func(mi, mj datedMessage) int {
		ti := mi.date.Time()
		tj := mj.date.Time()
		return ti.Compare(tj)
	})

	trim := strings.TrimSpace
	components := []string{trim(c.ChangeDescription(ci.ch))}
	for _, m := range dmsgs {
		components = append(components, trim(m.message))
	}
	return strings.Join(components, "\n\n"), nil
}

// relatedDocURL returns a unique URL for the document corresponding
// to the gerrit change info ci, intended for indexing documents used
// to surface related content.
func relatedDocURL(ci *changeInfo) string {
	return fmt.Sprintf("https://%s/c/%s/+/%d#related-content", ci.instance, ci.project, ci.number)
}

// comments returns file comments for the gerrit change.
func (c *Client) comments(ci *changeInfo) ([]*CommentInfo, error) {
	var cmts []*CommentInfo
	cmtsMap := c.Comments(ci.project, ci.number)
	for _, cs := range cmtsMap { // we don't care about comment file locations
		cmts = append(cmts, cs...)
	}
	return cmts, nil
}

// cleanBody should clean the body for indexing.
// For now we assume the LLM is good enough.
// In the future we may want to make various changes like inlining
// other mentioned changes, playground URLs, and GH issues.
// TODO(#35): remove irrelevant comments to fit the Gemini token limit.
func cleanBody(body string) string {
	return body
}

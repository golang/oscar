// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gerritdocs implements converting gerrit changes into text docs
// for [golang.org/x/oscar/internal/docs].
package gerritdocs

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/gerrit"
	"golang.org/x/oscar/internal/storage/timed"
)

// Sync writes to dc docs corresponding to each gerrit change that has
// been created or updated since the last call to Sync.
//
// A modification to a gerrit change will generate a new change info
// in gerrit. The state of the change will be written to dc, replacing
// the old change contents.
//
// Sync currently creates only one type of documents intended to be
// surfaced as a related content on new issues or other changes.
// This document consist of a change commit message and its comments.
// The ID for such documents is of the form
//
//	https://<gerrit-instance>/c/<repo>/+/<n>#related-content.
//
// The "#related-content" fragment is used to allow other types of
// gerrit documents to reuse the main portion of the change URL.
// The URL points to the top of the CL page since the fragment
// does not exist.
func Sync(ctx context.Context, lg *slog.Logger, dc *docs.Corpus, gr *gerrit.Client, projects []string) error {
	w := gr.ChangeWatcher("gerritrelateddocs")
	for ce := range w.Recent() {
		lg.Debug("gerritrelateddocs sync", "change", ce.ChangeNum, "dbtime", ce.DBTime)
		c := change(ce, gr, projects)
		if c == nil {
			lg.Error("gerritrelateddocs cannot find change", "change", ce.ChangeNum)
			continue
		}
		title := gr.ChangeSubject(c.ch)
		body, err := relatedDocBody(gr, c)
		if err != nil {
			lg.Error("gerritrelateddocs cannot find comments", "change", ce.ChangeNum)
			continue
		}
		if len(body) > geminiCharLimit {
			lg.Warn("gerritrelateddocs potential truncation by gemini", "change", ce.ChangeNum, "docSize", len(body))
		}
		text := cleanBody(body)
		id := relatedDocURL(gr, c)
		dc.Add(id, title, text)
		w.MarkOld(ce.DBTime)
	}
	return nil
}

// geminiCharLimit is an approximate limit on the number of
// document characters a gemini text embedding can accept.
// Gemini text embedding models have an input token limit
// of 2048, where each token is about four characters long.
// Gemini truncates documents after this limit.
// For more info, see
// https://ai.google.dev/gemini-api/docs/models/gemini#text-embedding-and-embedding
const geminiCharLimit = 8200

// changeInfo accumulates information from [gerrit.Change]
// and [gerrit.ChangeEvent] needed to grab change subject,
// messages, and comments.
type changeInfo struct {
	instance string
	project  string
	number   int
	ch       *gerrit.Change
}

// change returns a gerrit change information corresponding to ce.
// The project of the change must be one of projects.
func change(ce gerrit.ChangeEvent, gr *gerrit.Client, projects []string) *changeInfo {
	c := &changeInfo{
		instance: ce.Instance,
		number:   ce.ChangeNum,
	}
	for _, p := range projects {
		if ch := gr.Change(p, ce.ChangeNum); ch != nil {
			c.project = p // at most one project can match ce.ChangeNum
			c.ch = ch
			return c
		}
	}
	return nil
}

// relatedDocBody returns the document body for the gerrit change c,
// intended for surfacing related content. The body consists of
// the most recent commit message followed by change messages and
// comments appearing in their chronological order. There is a new
// line added between each message and comment.
func relatedDocBody(gr *gerrit.Client, c *changeInfo) (string, error) {
	comments, err := comments(gr, c)
	if err != nil {
		return "", nil
	}
	messages := gr.ChangeMessages(c.ch)

	// Sort comments and messages based on their creation/update time.
	type datedMessage struct {
		date    gerrit.TimeStamp
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
	components := []string{trim(gr.ChangeDescription(c.ch))}
	for _, m := range dmsgs {
		components = append(components, trim(m.message))
	}
	return strings.Join(components, "\n\n"), nil
}

// relatedDocURL returns a unique URL for the document corresponding
// to the gerrit change info c, intended for indexing documents used
// to surface related content.
func relatedDocURL(gr *gerrit.Client, c *changeInfo) string {
	return fmt.Sprintf("https://%s/c/%s/+/%d#related-content", c.instance, c.project, c.number)
}

// comments returns file comments for the gerrit change.
func comments(gr *gerrit.Client, c *changeInfo) ([]*gerrit.CommentInfo, error) {
	var cmts []*gerrit.CommentInfo
	cmtsMap, err := gr.Comments(c.project, c.number)
	if err != nil {
		return nil, err
	}
	for _, cs := range cmtsMap { // we don't care about comment file locations
		cmts = append(cmts, cs...)
	}
	return cmts, nil
}

// Restart causes the next call to Sync to behave as if
// it has never sync'ed any issues before.
// The result is that all issues will be reconverted to doc form
// and re-added.
// Docs that have not changed since the last addition to the corpus
// will appear unmodified; others will be marked new in the corpus.
func Restart(lg *slog.Logger, gr *gerrit.Client) {
	gr.ChangeWatcher("gerritrelateddocs").Restart()
}

// RelatedLatest returns the latest known DBTime marked old by
// the client's "gerritrelateddocs" Watcher.
func RelatedLatest(gr *gerrit.Client) timed.DBTime {
	return gr.ChangeWatcher("gerritrelateddocs").Latest()
}

// cleanBody should clean the body for indexing.
// For now we assume the LLM is good enough.
// In the future we may want to make various changes like inlining
// other mentioned changes, playground URLs, and GH issues.
// TODO(#35): remove irrelevant comments to fit the Gemini token limit.
func cleanBody(body string) string {
	return body
}

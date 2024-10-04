// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gerrit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"iter"
	"strconv"
	"time"

	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

// ChangeNumbers returns an iterator over the change numbers of a project.
// The first iterator value is the change number.
// The second iterator value is a function that can be called to
// return information about the change, as with [storage.DB.Scan].
func (c *Client) ChangeNumbers(project string) iter.Seq2[int, func() *Change] {
	return func(yield func(int, func() *Change) bool) {
		for key, fn := range c.db.Scan(o(changeKind, c.instance, project), o(changeKind, c.instance, project, ordered.Inf)) {
			var changeNum int
			if err := ordered.Decode(key, nil, nil, nil, &changeNum); err != nil {
				c.db.Panic("gerrit client change decode", "key", storage.Fmt(key), "err", err)
			}
			cfn := func() *Change {
				return &Change{
					num:  changeNum,
					data: bytes.Clone(fn()),
				}
			}
			if !yield(changeNum, cfn) {
				return
			}
		}
	}
}

// Change returns the data for a single change.
// This will return nil if no information is recorded.
func (c *Client) Change(project string, changeNum int) *Change {
	val, ok := c.db.Get(o(changeKind, c.instance, project, changeNum))
	if !ok {
		return nil
	}
	return &Change{
		num:  changeNum,
		data: val,
	}
}

// Comments returns the comments on a change, if any. These are the
// inline comments placed on files in the change. The top-level
// replies are stored in a [Change] and are returned by [Client.ChangeMessages].
//
// This returns a map from file names to a list of comments on each file.
// The result is nil if no comment information exists.
func (c *Client) Comments(project string, changeNum int) map[string][]*CommentInfo {
	val, ok := c.db.Get(o(commentKind, c.instance, project, changeNum))
	if !ok {
		return nil
	}

	// Unpack into a commentInfo struct, and then convert to CommentInfo,
	// so that we don't have to unpack the lengthy AccountInfo data
	// each time.
	type commentInfo struct {
		PatchSet        int                  `json:"patch_set,omitempty"`
		ID              string               `json:"id"`
		Path            string               `json:"path,omitempty"`
		Side            string               `json:"side,omitempty"`
		Parent          int                  `json:"parent,omitempty"`
		Line            int                  `json:"line,omitempty"`
		Range           *CommentRange        `json:"range,omitempty"`
		InReplyTo       string               `json:"in_reply_to,omitempty"`
		Message         string               `json:"message,omitempty"`
		Updated         TimeStamp            `json:"updated"`
		Author          json.RawMessage      `json:"author,omitempty"`
		Tag             string               `json:"tag,omitempty"`
		Unresolved      bool                 `json:"unresolved,omitempty"`
		ChangeMessageID string               `json:"change_message_id,omitempty"`
		CommitID        string               `json:"commit_id,omitempty"`
		FixSuggestions  []*FixSuggestionInfo `json:"fix_suggestions,omitempty"`
	}
	var comments map[string][]*commentInfo
	if err := json.Unmarshal(val, &comments); err != nil {
		c.slog.Error("gerrit comment decode failure", "num", changeNum, "data", val, "err", err)
		c.db.Panic("gerrit comment decode failure", "num", changeNum, "err", err)
	}

	ret := make(map[string][]*CommentInfo, len(comments))
	for key, val := range comments {
		rcs := make([]*CommentInfo, 0, len(val))
		for _, comment := range val {
			rc := &CommentInfo{
				PatchSet:        comment.PatchSet,
				ID:              comment.ID,
				Path:            comment.Path,
				Side:            comment.Side,
				Parent:          comment.Parent,
				Line:            comment.Line,
				Range:           comment.Range,
				InReplyTo:       comment.InReplyTo,
				Message:         comment.Message,
				Updated:         comment.Updated,
				Author:          c.loadAccount(comment.Author),
				Tag:             comment.Tag,
				Unresolved:      comment.Unresolved,
				ChangeMessageID: comment.ChangeMessageID,
				CommitID:        comment.CommitID,
				FixSuggestions:  comment.FixSuggestions,
			}
			rcs = append(rcs, rc)
		}
		ret[key] = rcs
	}

	return ret
}

// A ChangeEvent is a Gerrit CL change event returned by ChangeWatcher.
type ChangeEvent struct {
	DBTime    timed.DBTime // when event was created
	Instance  string       // Gerrit instance
	ChangeNum int          // change number
}

// ChangeWatcher returns a new [timed.Watcher] with the given name.
// It picks up where any previous Watcher of the same name left odd.
func (c *Client) ChangeWatcher(name string) *timed.Watcher[ChangeEvent] {
	return timed.NewWatcher(c.slog, c.db, name, changeUpdateKind, c.decodeChangeEvent)
}

// decodeChangeUpdateEntry decodes a changeUpdateKind [timed.Entry] into
// a change number.
func (c *Client) decodeChangeEvent(t *timed.Entry) ChangeEvent {
	ce := ChangeEvent{
		DBTime: t.ModTime,
	}
	if err := ordered.Decode(t.Key, &ce.Instance, &ce.ChangeNum, nil); err != nil {
		c.db.Panic("gerrit change event decode", "key", storage.Fmt(t.Key), "err", err)
	}
	return ce
}

// timeStampLayout is the timestamp format used by Gerrit.
// It is always in UTC.
const timeStampLayout = "2006-01-02 15:04:05.999999999"

// TimeStamp adds Gerrit timestamp JSON marshaling and unmarshaling
// to a [time.Time].
type TimeStamp time.Time

// MarshalJSON marshals a TimeStamp into JSON.
func (ts *TimeStamp) MarshalJSON() ([]byte, error) {
	return []byte(`"` + ts.Time().UTC().Format(timeStampLayout) + `"`), nil
}

// UnmarshalJSON unmarshals JSON into a TimeStamp.
func (ts *TimeStamp) UnmarshalJSON(p []byte) error {
	s, err := strconv.Unquote(string(p))
	if err != nil {
		return fmt.Errorf("failed to unquote Gerrit time stamp %q: %v", p, err)
	}
	t, err := time.Parse(timeStampLayout, s)
	if err != nil {
		return fmt.Errorf("failed to unmarshal Gerrit time stamp: %v", err)
	}
	*ts = TimeStamp(t)
	return nil
}

// Time returns the value of the TimeStamp as a [time.Time].
func (ts TimeStamp) Time() time.Time { return time.Time(ts) }

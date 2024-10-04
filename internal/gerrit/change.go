// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gerrit

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// A Change holds information for a single Gerrit change.
// This internally holds JSON encoded data,
// and only decodes what is needed.
type Change struct {
	num  int
	data []byte
}

// ChangeInfo returns a [ChangeInfo] holding almost all the information
// about a [Change]. This does not include the file comments,
// which can be retrieved using the [Client.Comments] method.
func (c *Client) ChangeInfo(ch *Change) *ChangeInfo {
	var ci ChangeInfo
	c.unmarshal(ch, "change", &ci)
	return &ci
}

// ChangeNumber returns the Gerrit change number.
// This is unique for a given Gerrit instance.
func (c *Client) ChangeNumber(ch *Change) int {
	return ch.num
}

// Status returns the status of the change: NEW, MERGED, ABANDONED.
func (c *Client) ChangeStatus(ch *Change) string {
	var status struct {
		Status string `json:"status"`
	}
	c.unmarshal(ch, "status", &status)
	return status.Status
}

// ChangeOwner returns the owner of the Gerrit change: the account that
// created the change.
func (c *Client) ChangeOwner(ch *Change) *AccountInfo {
	var owner struct {
		Owner json.RawMessage `json:"owner"`
	}
	c.unmarshal(ch, "owner", &owner)
	return c.loadAccount(owner.Owner)
}

// ChangeSubmitter returns the account that submitted the change.
// If the change has not been submitted this returns nil.
func (c *Client) ChangeSubmitter(ch *Change) *AccountInfo {
	var submitter struct {
		Submitter json.RawMessage `json:"submitter"`
	}
	c.unmarshal(ch, "submitter", &submitter)

	if len(submitter.Submitter) == 0 {
		return nil
	}
	return c.loadAccount(submitter.Submitter)
}

// ChangeTimes holds relevant times for a [Change].
type ChangeTimes struct {
	Created   time.Time // when change was created
	Updated   time.Time // when change was updated, zero if never
	Submitted time.Time // when change was submitted, zero if not
	Abandoned time.Time // when change was abandoned, zero if not
}

// ChangeTimes returns the created, updated, submitted, and abandoned times
// for a change. If the change is not submitted or not abandoned,
// those times will be zero.
func (c *Client) ChangeTimes(ch *Change) ChangeTimes {
	var times struct {
		Created   TimeStamp `json:"created"`
		Updated   TimeStamp `json:"updated"`
		Submitted TimeStamp `json:"submitted"`
		Status    string    `json:"status"`
	}
	c.unmarshal(ch, "times", &times)
	created := times.Created.Time()
	updated := times.Updated.Time()
	submitted := times.Submitted.Time()

	var abandoned time.Time
	if times.Status == "ABANDONED" {
		type message struct {
			Date    TimeStamp `json:"date"`
			Message string    `json:"message"`
		}
		var messages struct {
			Messages []message `json:"messages"`
		}
		c.unmarshal(ch, "abandoned messages", &messages)
		for _, msg := range slices.Backward(messages.Messages) {
			if strings.HasPrefix(msg.Message, "Abandoned") {
				abandoned = msg.Date.Time()
				break
			}
		}
		if abandoned.IsZero() {
			c.slog.Error("gerrit change abandoned missing message", "num", ch.num, "data", ch.data)
			c.db.Panic("gerrit change abandoned missing message", "num", ch.num)
		}
	}

	return ChangeTimes{
		Created:   created,
		Updated:   updated,
		Submitted: submitted,
		Abandoned: abandoned,
	}
}

// ChangeSubject returns the subject of a [Change].
// This is the first line of the change description.
func (c *Client) ChangeSubject(ch *Change) string {
	var subject struct {
		Subject string `json:"subject"`
	}
	c.unmarshal(ch, "subject", &subject)
	return subject.Subject
}

// ChangeMessages returns the messages on a [Change].
// These are the top-level messages created by clicking on
// the top REPLY button when reviewing a change.
// Inline file comments are returned by [Client.Comments].
func (c *Client) ChangeMessages(ch *Change) []*ChangeMessageInfo {
	var messages struct {
		Messages []json.RawMessage `json:"messages"`
	}
	c.unmarshal(ch, "messages", &messages)

	// Unpack into a changeMessageInfo struct, and then convert to
	// ChangeMessageInfo, so that we don't have to unpack the lengthy
	// AccountInfo data each time.
	type changeMessageInfo struct {
		ID                string            `json:"id"`
		Author            json.RawMessage   `json:"author,omitempty"`
		RealAuthor        json.RawMessage   `json:"real_author,omitempty"`
		Date              TimeStamp         `json:"date"`
		Message           string            `json:"message"`
		AccountsInMessage []json.RawMessage `json:"accounts_in_message,omitempty"`
		Tag               string            `json:"tag,omitempty"`
		RevisionNumber    int               `json:"_revision_number,omitempty"`
	}

	ret := make([]*ChangeMessageInfo, 0, len(messages.Messages))
	for _, msg := range messages.Messages {
		var cmi changeMessageInfo
		if err := json.Unmarshal(msg, &cmi); err != nil {
			c.slog.Error("gerrit message decode failure", "num", ch.num, "data", msg, "err", err)
			c.db.Panic("gerrit message decode failure", "num", ch.num, "err", err)
		}

		var aims []*AccountInfo
		for _, aim := range cmi.AccountsInMessage {
			aims = append(aims, c.loadAccount(aim))
		}
		r := &ChangeMessageInfo{
			ID:                cmi.ID,
			Author:            c.loadAccount(cmi.Author),
			RealAuthor:        c.loadAccount(cmi.RealAuthor),
			Date:              cmi.Date,
			Message:           cmi.Message,
			AccountsInMessage: aims,
			Tag:               cmi.Tag,
			RevisionNumber:    cmi.RevisionNumber,
		}
		ret = append(ret, r)
	}

	return ret
}

// ChangeDescription returns the current description of the change.
func (c *Client) ChangeDescription(ch *Change) string {
	type commitInfo struct {
		Message string `json:"message"`
	}
	type revisionInfo struct {
		Commit commitInfo `json:"commit"`
	}
	var revisions struct {
		CurrentRevision string                  `json:"current_revision"`
		Revisions       map[string]revisionInfo `json:"revisions"`
	}
	c.unmarshal(ch, "current revision", &revisions)

	rev, ok := revisions.Revisions[revisions.CurrentRevision]
	if !ok {
		c.slog.Error("gerrit no revision data for current revision", "num", ch.num, "data", ch.data, "currentRevision", revisions.CurrentRevision)
		c.db.Panic("gerrit no revision data for current revision", "num", ch.num)
	}

	return rev.Commit.Message
}

// ChangeWorkInProgress reports whether the change is marked as
// work-in-progress.
func (c *Client) ChangeWorkInProgress(ch *Change) bool {
	var workInProgress struct {
		WorkInProgress bool `json:"work_in_progress"`
	}
	c.unmarshal(ch, "work in progress", &workInProgress)
	return workInProgress.WorkInProgress
}

// ChangeReviewers returns a list of accounts that are listed as
// reviewers of this change.
// Note that this is not identical to ChangeInfo.Reviewers,
// which includes both reviewers and people CC'ed.
func (c *Client) ChangeReviewers(ch *Change) []*AccountInfo {
	var reviewers struct {
		Reviewers map[string][]json.RawMessage `json:"reviewers"`
	}
	c.unmarshal(ch, "reviewed", &reviewers)

	revs := reviewers.Reviewers["REVIEWER"]
	if len(revs) == 0 {
		return nil
	}

	ret := make([]*AccountInfo, 0, len(revs))
	for _, rev := range revs {
		ret = append(ret, c.loadAccount(rev))
	}
	return ret
}

// ChangeLabels returns a map from label names to LabelInfo values.
func (c *Client) ChangeLabels(ch *Change) map[string]*LabelInfo {
	var labels struct {
		Labels map[string]json.RawMessage `json:"labels"`
	}
	c.unmarshal(ch, "labels", &labels)

	ret := make(map[string]*LabelInfo, len(labels.Labels))
	for name, msg := range labels.Labels {
		ret[name] = c.unmarshalLabel(ch, msg)
	}
	return ret
}

// ChangeLabel returns information about a label.
// It returns nil if that label is not present.
func (c *Client) ChangeLabel(ch *Change, label string) *LabelInfo {
	var labels struct {
		Labels map[string]json.RawMessage `json:"labels"`
	}
	c.unmarshal(ch, "labels", &labels)
	msg, ok := labels.Labels[label]
	if !ok {
		return nil
	}
	return c.unmarshalLabel(ch, msg)
}

// unmarshalLabel unmarshals a LabelInfo.
func (c *Client) unmarshalLabel(ch *Change, input json.RawMessage) *LabelInfo {
	// Unpack into approvalInfo and labelInfo structs, and then convert to
	// ApprovalInfo and LabelInfo, so that we don't have to unpack the
	// lengthy AccountInfo data each time.
	type approvalInfo struct {
		Value                int             `json:"value,omitempty"`
		PermittedVotingRange VotingRangeInfo `json:"permitted_voting_range,omitempty"`
		Date                 TimeStamp       `json:"date,omitempty"`
		Tag                  string          `json:"tag,omitempty"`
		PostSubmit           bool            `json:"post_submit,omitempty"`
	}
	type labelInfo struct {
		Optional     bool              `json:"optional,omitempty"`
		Description  string            `json:"description,omitempty"`
		Approved     json.RawMessage   `json:"approved,omitempty"`
		Rejected     json.RawMessage   `json:"rejected,omitempty"`
		Recommended  json.RawMessage   `json:"recommended,omitempty"`
		Disliked     json.RawMessage   `json:"disliked,omitempty"`
		Blocking     bool              `json:"blocking,omitempty"`
		Value        int               `json:"value,omitempty"`
		DefaultValue int               `json:"default_value,omitempty"`
		Votes        []int             `json:"votes,omitempty"`
		All          []json.RawMessage `json:"all,omitempty"`
		Values       map[string]string `json:"values,omitempty"`
	}

	var li labelInfo
	if err := json.Unmarshal(input, &li); err != nil {
		c.slog.Error("gerrit label info decode failure", "num", ch.num, "data", ch.data, "err", err)
		c.db.Panic("gerrit label info decode failure", "num", ch.num, "err", err)
	}

	all := make([]*ApprovalInfo, 0, len(li.All))
	for _, aai := range li.All {
		ac := c.loadAccount(aai)
		var jai approvalInfo
		if err := json.Unmarshal(aai, &jai); err != nil {
			c.slog.Error("gerrit label approval decode failure", "num", ch.num, "data", ch.data, "err", err)
			c.db.Panic("gerrit label approval decode failure", "num", ch.num, "err", err)
		}
		bai := &ApprovalInfo{
			AccountInfo:          ac,
			Value:                jai.Value,
			PermittedVotingRange: jai.PermittedVotingRange,
			Date:                 jai.Date,
			Tag:                  jai.Tag,
			PostSubmit:           jai.PostSubmit,
		}
		all = append(all, bai)
	}

	return &LabelInfo{
		Optional:     li.Optional,
		Description:  li.Description,
		Approved:     c.loadAccount(li.Approved),
		Rejected:     c.loadAccount(li.Rejected),
		Recommended:  c.loadAccount(li.Recommended),
		Disliked:     c.loadAccount(li.Disliked),
		Blocking:     li.Blocking,
		Value:        li.Value,
		DefaultValue: li.DefaultValue,
		Votes:        li.Votes,
		All:          all,
		Values:       li.Values,
	}
}

// ChangeCommitAuthor returns the author of a given patch set number
// of a change. If the patch set number does not exist or the information
// is missing, this returns nil.
func (c *Client) ChangeCommitAuthor(ch *Change, patchset int) *GitPersonInfo {
	var revisions struct {
		Revisions map[string]json.RawMessage `json:"revisions"`
	}
	c.unmarshal(ch, "revisions", &revisions)
	for _, rev := range revisions.Revisions {
		var number struct {
			Number int `json:"_number"`
		}
		if err := json.Unmarshal(rev, &number); err != nil {
			c.slog.Error("gerrit revision number decode failure", "num", ch.num, "data", ch.data, "err", err)
			c.db.Panic("gerrit revision number decode failure", "num", ch.num, "err", err)
		}
		if number.Number != patchset {
			continue
		}

		type commitInfo struct {
			Author *GitPersonInfo `json:"author"`
		}
		var commit struct {
			Commit *commitInfo `json:"commit"`
		}
		if err := json.Unmarshal(rev, &commit); err != nil {
			c.slog.Error("gerrit revision commit decode failure", "num", ch.num, "data", ch.data, "err", err)
			c.db.Panic("gerrit revision commit decode failure", "num", ch.num, "err", err)
		}
		if commit.Commit == nil {
			return nil
		}
		return commit.Commit.Author
	}
	return nil
}

// ChangeHashTags returns the list of hashtags set on the change.
func (c *Client) ChangeHashtags(ch *Change) []string {
	var hashtags struct {
		Hashtags []string `json:"hashtags"`
	}
	c.unmarshal(ch, "hashtags", &hashtags)
	return hashtags.Hashtags
}

// ChangeCommentCounts returns the total number of comments and the
// nmber of unresolved comments.
func (c *Client) ChangeCommentCounts(ch *Change) (total, unresolved int) {
	var counts struct {
		TotalCommentCount      int `json:"total_comment_count"`
		UnresolvedCommentCount int `json:"unresolved_comment_count"`
	}
	c.unmarshal(ch, "comment counts", &counts)
	return counts.TotalCommentCount, counts.UnresolvedCommentCount
}

// ChangeRevisions returns information about all the patch sets,
// ordered by patch set number.
func (c *Client) ChangeRevisions(ch *Change) []*RevisionInfo {
	// Unpack into commitInfo and revisionInfo structs,
	// and then convert to CommitInfo and RevisionInfo,
	// so that we don't have to unpack the lengthy AccountInfo
	// data each time. We also skip some CommitInfo fields.
	type commitInfo struct {
		Commit    string         `json:"commit,omitempty"`
		Author    *GitPersonInfo `json:"author,omitempty"`
		Committer *GitPersonInfo `json:"committer,omitempty"`
		Subject   string         `json:"subject"`
		Message   string         `json:"message,omitempty"`
	}
	type revisionInfo struct {
		Kind         string          `json:"kind"`
		Number       int             `json:"_number"`
		Created      TimeStamp       `json:"created"`
		Uploader     json.RawMessage `json:"uploader"`
		RealUploader json.RawMessage `json:"real_uploader,omitempty"`
		Ref          string          `json:"ref"`
		Commit       *commitInfo     `json:"commit,omitempty"`
		Branch       string          `json:"branch,omitempty"`
		Description  string          `json:"description,omitempty"`
	}
	var revisions struct {
		Revisions map[string]*revisionInfo `json:"revisions"`
	}
	c.unmarshal(ch, "revisions", &revisions)

	toCommitInfo := func(from *commitInfo) *CommitInfo {
		if from == nil {
			return nil
		}
		return &CommitInfo{
			Commit:    from.Commit,
			Author:    from.Author,
			Committer: from.Committer,
			Subject:   from.Subject,
			Message:   from.Message,
		}
	}

	revs := make([]*RevisionInfo, 0, len(revisions.Revisions))
	for _, rev := range revisions.Revisions {
		ri := &RevisionInfo{
			Kind:         rev.Kind,
			Number:       rev.Number,
			Created:      rev.Created,
			Uploader:     c.loadAccount(rev.Uploader),
			RealUploader: c.loadAccount(rev.RealUploader),
			Ref:          rev.Ref,
			Commit:       toCommitInfo(rev.Commit),
			Branch:       rev.Branch,
			Description:  rev.Description,
		}
		revs = append(revs, ri)
	}

	slices.SortFunc(revs, func(a, b *RevisionInfo) int {
		return cmp.Compare(a.Number, b.Number)
	})

	return revs
}

// unmarshal unmarshals ch.data into a value. If the unmarshal fails, it
// crashes with an error.
func (c *Client) unmarshal(ch *Change, msg string, val any) {
	if err := json.Unmarshal(ch.data, val); err != nil {
		c.slog.Error(fmt.Sprintf("gerrit %s decode failure", msg), "num", ch.num, "data", ch.data, "err", err)
		c.db.Panic(fmt.Sprintf("gerrit %s decode failure", msg), "num", ch.num, "err", err)
	}
}

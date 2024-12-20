// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package overview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/storage"
)

// an action is a post or update action to be taken by a [poster].
// It has all the information needed to post or update a comment
// (and its corresponding link in the issue body) to a GitHub issue.
type action struct {
	Issue       *github.Issue               // the issue to modify
	LastComment int64                       // the ID of the last comment considered in generating the overview
	Changes     *github.IssueCommentChanges // the comment to post
	// If the following is nil, this a first post.
	// Otherwise, it is an update.
	IssueComment *github.IssueComment // the comment to modify
}

// isPost reports whether this action is a first post action.
// (If not, it is an update action).
func (a *action) isPost() bool {
	return a.IssueComment == nil
}

// result is the result of applying an [action].
type result struct {
	URL string // URL of the poster's comment
}

// getAction returns the action to take on the issue.
// It returns a post action if there is no existing overview comment,
// and an update action otherwise.
func (p *poster) getAction(ctx context.Context, iss *github.Issue, getOverview overviewFunc) (*action, error) {
	oc, err := p.findOverviewComment(iss)
	if err != nil {
		return nil, err
	}
	r, err := getOverview(ctx, iss)
	if err != nil {
		return nil, err
	}
	changes := &github.IssueCommentChanges{
		Body: comment(r.Overview.Response),
	}
	return &action{
		Issue:        iss,
		LastComment:  r.LastComment,
		Changes:      changes,
		IssueComment: oc,
	}, nil
}

// actioner implements [actions.Actioner].
type actioner struct {
	p *poster
}

// Implements [actions.Actioner.Run].
func (ar *actioner) Run(ctx context.Context, data []byte) ([]byte, error) {
	return runFromActionLog(ctx, data, ar.p.runAction)
}

// Implements [actions.Actioner.ForDisplay].
func (ar *actioner) ForDisplay(data []byte) string {
	a, err := decodeAction(data)
	if err != nil {
		return fmt.Sprintf("ERROR: %v", err)
	}
	if a.isPost() {
		return "post issue comment (and add link) to: " + a.Issue.HTMLURL + "\nnew comment:\n" + a.Changes.Body
	}
	return "update issue comment: " + a.IssueComment.HTMLURL + "\nupdated comment:\n" + a.Changes.Body
}

// decodeAction unmarshals the JSON into an action.
func decodeAction(b []byte) (*action, error) {
	var action action
	if err := json.Unmarshal(b, &action); err != nil {
		return nil, err
	}
	return &action, nil
}

// encode marshals an action into JSON.
func (a *action) encode() []byte {
	return storage.JSON(a)
}

// runFromActionLog is called by [actioner.Run] to execute an action.
// It decodes the action, calls [runAction], then encodes the result.
func runFromActionLog(ctx context.Context, data []byte,
	runAction func(context.Context, *action) (*result, error)) ([]byte, error) {
	a, err := decodeAction(data)
	if err != nil {
		return nil, err
	}
	res, err := runAction(ctx, a)
	if err != nil {
		return nil, err
	}
	return storage.JSON(res), nil
}

var (
	errStaleAction            = errors.New("stale update action")
	errEditIssueCommentFailed = errors.New("edit issue comment failed")
	errPostIssueCommentFailed = errors.New("post issue comment failed")
	errDownloadIssueFailed    = errors.New("download issue failed")
	errEditIssueFailed        = errors.New("edit issue failed")
)

// runAction runs the given action.
//
// If GitHub returns an error, add it to the action log for this action.
// It is unclear what the right behavior is, but at least at present all
// failed actions are available to the program and could be re-run.
func (p *poster) runAction(ctx context.Context, a *action) (*result, error) {
	if a.isPost() {
		return p.runPostAction(ctx, a)
	}
	return p.runUpdateAction(ctx, a)
}

// runPostAction runs a post action (post new comment and add link to it from the issue body).
// It returns an error if posting the comment fails.
// If adding the link to issue body fails, it logs the error but does not return it.
// This part of the action will be re-tried on the next update.
func (p *poster) runPostAction(ctx context.Context, a *action) (*result, error) {
	p.slog.Info("overview: running post action", "action", a)
	_, url, err := p.gh.PostIssueComment(ctx, a.Issue, a.Changes)
	if err != nil {
		return nil, fmt.Errorf("%w issue=%d: %v", errPostIssueCommentFailed, a.Issue.Number, err)
	}
	if err := p.addLinkToComment(ctx, a.Issue, url); err != nil {
		// A failure here is not fatal, as it will be re-tried when the overview is updated.
		p.slog.Error("overview: could not add link to comment", "error", err)
	}
	return &result{URL: url}, nil
}

// addLinkToComment adds a message linking to the given url in the body of the given
// issue (if not already present).
func (p *poster) addLinkToComment(ctx context.Context, iss *github.Issue, url string) error {
	// Avoid downloading the issue if our version already has a link to the comment.
	// If the link happens to be deleted in the intervening time,
	// we will try again next time the overview is updated.
	if p.containsCommentURL(iss, url) {
		p.slog.Info("overview: cached issue already has link to comment", "issue", iss.Number)
		return nil
	}
	liveIss, err := p.gh.DownloadIssue(ctx, iss.URL)
	if err != nil {
		return fmt.Errorf("%w issue=%d: %v", errDownloadIssueFailed, iss.Number, err)
	}
	// Check the live version.
	if p.containsCommentURL(liveIss, url) {
		p.slog.Info("overview: live issue already has link to comment", "issue", iss.Number)
		return nil
	}
	// Unfortunately, if the issue body is edited between the call to
	// DownloadIssue and EditIssue, we will overwrite the edits.
	// There's no clear way to prevent this.
	err = p.gh.EditIssue(ctx, liveIss, &github.IssueChanges{Body: appendCommentURL(liveIss, p.bot, url)})
	if err != nil {
		return fmt.Errorf("%w issue=%d: %v", errEditIssueFailed, liveIss.Number, err)
	}
	p.slog.Info("overview: added link to overview comment to issue body", "issue", iss.Number, "url", url)
	return nil
}

// runUpdateAction runs the update action (edit comment and update link in issue
// body if needed).
// It returns an error if the update action is stale (there is newer update action for this
// issue), or if the update fails.
// If adding/updating the link to issue body fails, it logs the error but does not return it.
// This part of the action will be re-tried on the next update.
func (p *poster) runUpdateAction(ctx context.Context, a *action) (*result, error) {
	p.slog.Info("overview: running update action", "action", a)
	// Check if the update action is stale.
	// This happens if a newer update action is created while an older update action is
	// still waiting for approval.
	project, issue := a.Issue.Project(), a.Issue.Number
	k := string(p.issueStateKey(project, issue))
	p.db.Lock(k)
	if lc := p.lastComment(project, issue); lc > a.LastComment {
		p.db.Unlock(k)
		return nil, fmt.Errorf("%w issue=%d, (last comment in action = %d, last comment in db = %d)", errStaleAction,
			issue, a.LastComment, lc)
	}
	p.db.Unlock(k)

	// Update the issue comment.
	// Note that we don't need to check whether the live version of
	// the issue comment matches our version.
	// This poster "owns" this comment and may freely overwrite edits
	// from itself or other entities.
	err := p.gh.EditIssueComment(ctx, a.IssueComment, a.Changes)
	if err != nil {
		return nil, fmt.Errorf("%w issue=%d, comment=%s: %v", errEditIssueCommentFailed,
			a.IssueComment.Issue(), a.IssueComment.HTMLURL, err)
	}

	if err := p.addLinkToComment(ctx, a.Issue, a.IssueComment.HTMLURL); err != nil {
		// A failure here is not fatal, as it will be re-tried next time the overview is updated.
		p.slog.Error("overview: could not add link to comment", "error", err)
	}

	return &result{URL: a.IssueComment.HTMLURL}, nil
}

// containsCommentURL reports whether the body of the issue contains the given url.
func (p *poster) containsCommentURL(iss *github.Issue, url string) bool {
	return strings.Contains(iss.Body, url)
}

// appendCommentURL appends a message linking to the given url to the issue body and
// returns the result.
func appendCommentURL(iss *github.Issue, bot string, url string) string {
	// OK to modify editText (though it must contain url).
	editText := fmt.Sprintf("@%s's overview of this issue: %s", bot, url)
	return strings.Join([]string{
		iss.Body,
		editText,
	}, "\n")
}

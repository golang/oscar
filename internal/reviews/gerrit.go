// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package reviews

import (
	"context"
	"iter"
	"slices"
	"strconv"
	"time"

	"golang.org/x/oscar/internal/gerrit"
)

// GerritReviewClient is a [gerrit.Client] with a mapping
// from account e-mail addresses to [Account] data.
// We do things this way because a lot of Gerrit change
// data is more or less the same for any Gerrit instance,
// but account information is not.
type GerritReviewClient struct {
	GClient  *gerrit.Client
	Accounts AccountLookup
}

// GerritChange implements [Change] for a Gerrit CL.
type GerritChange struct {
	Client *GerritReviewClient
	Change *gerrit.Change
}

// ID returns the change ID.
func (gc *GerritChange) ID(ctx context.Context) string {
	return strconv.Itoa(gc.Client.GClient.ChangeNumber(gc.Change))
}

// Status returns the change status.
func (gc *GerritChange) Status(ctx context.Context) Status {
	switch gc.Client.GClient.ChangeStatus(gc.Change) {
	case "MERGED":
		return StatusSubmitted
	case "ABANDONED":
		return StatusClosed
	default:
		if gc.Client.GClient.ChangeWorkInProgress(gc.Change) {
			return StatusDoNotReview
		}
		return StatusReady
	}
}

// Author returns the change author.
func (gc *GerritChange) Author(ctx context.Context) Account {
	gai := gc.Client.GClient.ChangeOwner(gc.Change)
	return gc.Client.Accounts.Lookup(ctx, gai.Email)
}

// Created returns the time that the change was created.
func (gc *GerritChange) Created(ctx context.Context) time.Time {
	ct := gc.Client.GClient.ChangeTimes(gc.Change)
	return ct.Created
}

// Updated returns the time that the change was last updated.
func (gc *GerritChange) Updated(ctx context.Context) time.Time {
	ct := gc.Client.GClient.ChangeTimes(gc.Change)
	return ct.Updated
}

// UpdatedByAuthor returns the time that the change was updated by the
// original author.
func (gc *GerritChange) UpdatedByAuthor(ctx context.Context) time.Time {
	author := gc.Client.GClient.ChangeOwner(gc.Change)
	revs := gc.Client.GClient.ChangeRevisions(gc.Change)
	for _, rev := range slices.Backward(revs) {
		if rev.Uploader.Email == author.Email {
			return rev.Created.Time()
		}
	}
	return time.Time{}
}

// Subject returns the change subject.
func (gc *GerritChange) Subject(ctx context.Context) string {
	return gc.Client.GClient.ChangeSubject(gc.Change)
}

// Description returns the full change description.
func (gc *GerritChange) Description(ctx context.Context) string {
	return gc.Client.GClient.ChangeDescription(gc.Change)
}

// Reviewers returns the assigned reviewers.
func (gc *GerritChange) Reviewers(ctx context.Context) []Account {
	reviewers := gc.Client.GClient.ChangeReviewers(gc.Change)
	ret := make([]Account, 0, len(reviewers))
	for _, rev := range reviewers {
		ret = append(ret, gc.Client.Accounts.Lookup(ctx, rev.Email))
	}
	return ret
}

// Reviewed returns the accounts that have reviewed the change.
// We treat any account that has sent a message about the change
// as a reviewer.
func (gc *GerritChange) Reviewed(ctx context.Context) []Account {
	reviewers := make(map[string]bool)
	msgs := gc.Client.GClient.ChangeMessages(gc.Change)
	for _, msg := range msgs {
		if msg.RealAuthor != nil {
			reviewers[msg.RealAuthor.Email] = true
		} else if msg.Author != nil {
			reviewers[msg.Author.Email] = true
		}
	}

	num := gc.Client.GClient.ChangeNumber(gc.Change)
	project := gc.Client.GClient.ChangeProject(gc.Change)
	commentMap := gc.Client.GClient.Comments(project, num)
	for _, comments := range commentMap {
		for _, comment := range comments {
			if comment.Author != nil {
				reviewers[comment.Author.Email] = true
			}
		}
	}

	owner := gc.Client.GClient.ChangeOwner(gc.Change)
	delete(reviewers, owner.Email)

	ret := make([]Account, 0, len(reviewers))
	for email := range reviewers {
		ret = append(ret, gc.Client.Accounts.Lookup(ctx, email))
	}
	return ret
}

// Needs returns missing requirements for submittal.
// This implementation does what we can, but most projects will need
// their own version of this method.
func (gc *GerritChange) Needs(ctx context.Context) Needs {
	hasReview, hasMaintainerReview := false, false
	for _, review := range gc.Reviewed(ctx) {
		switch review.Authority(ctx) {
		case AuthorityReviewer:
			hasReview = true
		case AuthorityMaintainer, AuthorityOwner:
			hasMaintainerReview = true
		}
	}
	if hasMaintainerReview {
		// We don't really know if the change can be submitted.
		return 0
	} else if hasReview {
		return NeedsMaintainerReview
	} else {
		return NeedsReview
	}
}

// GerritChanges converts from an iterator over [gerrit.Change]
// values into an iterator over [GerritChange] values.
func GerritChanges(cl *gerrit.Client, accounts AccountLookup, it iter.Seq[*gerrit.Change]) iter.Seq[*GerritChange] {
	return func(yield func(*GerritChange) bool) {
		grc := &GerritReviewClient{
			GClient:  cl,
			Accounts: accounts,
		}
		for ch := range it {
			gc := &GerritChange{
				Client: grc,
				Change: ch,
			}
			if !yield(gc) {
				return
			}
		}
	}
}

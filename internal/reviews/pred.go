// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package reviews

import (
	"context"
	"fmt"
	"slices"
)

// A Predicate is a categorization of a [Change].
// An example of a Predicate would be "has been approved by a maintainer"
// or "author is a known contributor" or
// "waiting for response from author."
// The dashboard permits filtering and sorting CLs based on the different
// predicates they satisfy.
// A Predicate has a default score that indicates how important it is,
// but the dashboard permits using different sort orders.
type Predicate struct {
	Name        string // a short, one-word name
	Description string // a longer description
	Score       int    // default scoring value

	// The Applies function reports whether this Predicate
	// applies to a change.
	Applies func(context.Context, Change) (bool, error)
}

// ChangePreds is a [Change] with a list of predicates that apply
// to that change.
type ChangePreds struct {
	Change     Change
	Predicates []*Predicate
}

// A Reject is like a [Predicate], but if the Reject applies to a [Change]
// then the Change is not put on the dashboard at all.
type Reject Predicate

// ApplyPredicates takes a [Change] and applies predicates to it.
// The reject predicates are used to determine if the change is
// reviewable; the bool result will be false if the change should
// not be reviewed, for example because it has already been committed.
func ApplyPredicates(ctx context.Context, change Change, predicates []Predicate, rejects []Reject) (ChangePreds, bool, error) {
	for i := range rejects {
		applies, err := rejects[i].Applies(ctx, change)
		if err != nil {
			return ChangePreds{}, false, err
		}
		if applies {
			return ChangePreds{}, false, nil
		}
	}

	var preds []*Predicate
	for i := range predicates {
		pred := &predicates[i]
		applies, err := pred.Applies(ctx, change)
		if err != nil {
			return ChangePreds{}, false, err
		}
		if applies {
			preds = append(preds, pred)
		}
	}

	cp := ChangePreds{
		Change:     change,
		Predicates: preds,
	}

	return cp, true, nil
}

// Some [Predicate] default scores.
const (
	ScoreImportant     = 10  // change is important
	ScoreSuggested     = 1   // change is worth looking at
	ScoreUninteresting = -1  // change is not interesting
	ScoreUnimportant   = -10 // change is not important
)

// Predicates returns a list of non-reject predicates
// that apply to a change.
func Predicates() []Predicate {
	return slices.Clone(predicates)
}

// predicates is the list of [Predicate] values that we apply to a change.
var predicates = []Predicate{
	{
		Name:        "authorMaintainer",
		Description: "the change author is a project maintainer",
		Score:       ScoreImportant,
		Applies:     authorMaintainer,
	},
	{
		Name:        "authorReviewer",
		Description: "the change author is a project reviewer",
		Score:       ScoreSuggested,
		Applies:     authorReviewer,
	},
	{
		Name:        "authorContributor",
		Description: "the change author is a project contributor",
		Score:       ScoreSuggested,
		Applies:     authorContributor,
	},
	{
		Name:        "authorMajorContributor",
		Description: "the change author is a major project contributor",
		Score:       ScoreImportant,
		Applies:     authorMajorContributor,
	},
	{
		Name:        "noMaintainerReviews",
		Description: "has no reviews from a project maintainer",
		Score:       ScoreSuggested,
		Applies:     noMaintainerReviews,
	},
	{
		Name:        "mergeConflict",
		Description: "has a merge conflict",
		Score:       ScoreUninteresting,
		Applies:     mergeConflict,
	},
}

// authorMaintainer is a [Predicate] function that reports whether the
// [Change] author is a project maintainer.
func authorMaintainer(ctx context.Context, ch Change) (bool, error) {
	switch ch.Author(ctx).Authority(ctx) {
	case AuthorityMaintainer, AuthorityOwner:
		return true, nil
	default:
		return false, nil
	}
}

// authorReviewer is a [Predicate] function that reports whether the
// [Change] author is a project reviewer.
func authorReviewer(ctx context.Context, ch Change) (bool, error) {
	switch ch.Author(ctx).Authority(ctx) {
	case AuthorityReviewer:
		return true, nil
	default:
		return false, nil
	}
}

// authorContributor is a [Predicate] function that reports whether the
// [Change] author is a known contributor: more than 10 changes contributed.
func authorContributor(ctx context.Context, ch Change) (bool, error) {
	return ch.Author(ctx).Commits(ctx) > 10, nil
}

// authorMajorContributor is a [Predicate] function that reports whether the
// [Change] author is a major contributor: more than 50 changes contributed.
func authorMajorContributor(ctx context.Context, ch Change) (bool, error) {
	return ch.Author(ctx).Commits(ctx) > 50, nil
}

// noMaintainerReviews is a [Predicate] function that reports whether the
// [Change] has not been reviewed by a maintainer.
func noMaintainerReviews(ctx context.Context, ch Change) (bool, error) {
	for _, r := range ch.Reviewed(ctx) {
		switch r.Authority(ctx) {
		case AuthorityMaintainer, AuthorityOwner:
			return false, nil
		}
	}
	return true, nil
}

// mergeConflict is a [Predicate] function that reports whether the
// [Change] has a merge conflict.
func mergeConflict(ctx context.Context, ch Change) (bool, error) {
	conflict := ch.Needs(ctx)&NeedsConflictResolve != 0
	return conflict, nil
}

// Rejects returns a list of reject predicates
// that apply to a change.
func Rejects() []Reject {
	return slices.Clone(rejects)
}

// rejects is the list of Reject values that we apply to a change.
var rejects = []Reject{
	{
		Name:        "reviewable",
		Description: "whether the change is reviewable",
		Applies:     unreviewable,
	},
}

// unreviewable is a [Reject] function that reports whether a
// [Change] is not reviewable.
func unreviewable(ctx context.Context, ch Change) (bool, error) {
	switch status := ch.Status(ctx); status {
	case StatusReady:
		return false, nil
	case StatusSubmitted, StatusClosed, StatusDoNotReview:
		return true, nil
	default:
		return false, fmt.Errorf("reviewable predicate: change %s: unrecognized status %d", ch.ID(ctx), status)
	}
}

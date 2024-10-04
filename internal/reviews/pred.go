// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package reviews

import (
	"fmt"
	"sync"
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
	Applies func(Change) (bool, error)
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
// The bool result reports whether the change is reviewable;
// this will be false if the change should not be reviewed,
// for example because it has already been committed.
func ApplyPredicates(change Change) (ChangePreds, bool, error) {
	predicatesLock.Lock()
	// The predicates and rejects slices are append-only,
	// so a top-level copy is safe to use.
	p := predicates
	r := rejects
	predicatesLock.Unlock()

	for i := range r {
		applies, err := r[i].Applies(change)
		if err != nil {
			return ChangePreds{}, false, err
		}
		if applies {
			return ChangePreds{}, false, nil
		}
	}

	var preds []*Predicate
	for i := range p {
		pred := &p[i]
		applies, err := pred.Applies(change)
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

// AddPredicates adds more [Predicate] values for a project.
func AddPredicates(newPreds []Predicate) {
	predicatesLock.Lock()
	defer predicatesLock.Unlock()
	predicates = append(predicates, newPreds...)
}

// AddRejects adds more [Reject] values for a project.
func AddRejects(newRejects []Reject) {
	predicatesLock.Lock()
	defer predicatesLock.Unlock()
	rejects = append(rejects, newRejects...)
}

// Some [Predicate] default scores.
const (
	ScoreImportant     = 10  // change is important
	ScoreSuggested     = 1   // change is worth looking at
	ScoreUninteresting = -1  // change is not interesting
	ScoreUnimportant   = -10 // change is not important
)

// predicatesLock protects predicates and rejectors.
var predicatesLock sync.Mutex

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
}

// authorMaintainer is a [Predicate] function that reports whether the
// [Change] author is a project maintainer.
func authorMaintainer(ch Change) (bool, error) {
	switch ch.Author().Authority() {
	case AuthorityMaintainer, AuthorityOwner:
		return true, nil
	default:
		return false, nil
	}
}

// authorReviewer is a [Predicate] function that reports whether the
// [Change] author is a project reviewer.
func authorReviewer(ch Change) (bool, error) {
	switch ch.Author().Authority() {
	case AuthorityReviewer:
		return true, nil
	default:
		return false, nil
	}
}

// authorContributor is a [Predicate] function that reports whether the
// [Change] author is a known contributor: more than 10 changes contributed.
func authorContributor(ch Change) (bool, error) {
	return ch.Author().Commits() > 10, nil
}

// authorMajorContributor is a [Predicate] function that reports whether the
// [Change] author is a major contributor: more than 50 changes contributed.
func authorMajorContributor(ch Change) (bool, error) {
	return ch.Author().Commits() > 50, nil
}

// noMaintainerReviews is a [Predicate] function that reports whether the
// [Change] has not been reviewed by a maintainer.
func noMaintainerReviews(ch Change) (bool, error) {
	for _, r := range ch.Reviewed() {
		switch r.Authority() {
		case AuthorityMaintainer, AuthorityOwner:
			return false, nil
		}
	}
	return true, nil
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
func unreviewable(ch Change) (bool, error) {
	switch status := ch.Status(); status {
	case StatusReady:
		return false, nil
	case StatusSubmitted, StatusClosed, StatusDoNotReview:
		return true, nil
	default:
		return false, fmt.Errorf("reviewable predicate: change %s: unrecognized status %d", ch.ID(), status)
	}
}

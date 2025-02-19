// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package goreviews

import (
	"context"
	"slices"
	"strings"

	"golang.org/x/oscar/internal/gerrit"
	"golang.org/x/oscar/internal/reviews"
)

// predicates is a list of [reviews.Predicate] classifiers
// that are specific to Go.
var predicates = []reviews.Predicate{
	{
		Name:        "hasPlusTwo",
		Description: "change has a +2 vote",
		Score:       reviews.ScoreImportant,
		Applies:     hasPlusTwo,
	},
	{
		Name:        "hasPlusOne",
		Description: "change has a +1 vote",
		Score:       reviews.ScoreSuggested,
		Applies:     hasPlusOne,
	},
	{
		Name:        "hasUnresolvedComments",
		Description: "change has unresolved comments",
		Score:       reviews.ScoreUninteresting,
		Applies:     hasUnresolvedComments,
	},
	{
		Name:        "trybotsPassed",
		Description: "trybots passed",
		Score:       reviews.ScoreSuggested,
		Applies:     trybotsPassed,
	},
	{
		Name:        "trybotsFailed",
		Description: "trybots failed",
		Score:       reviews.ScoreUninteresting,
		Applies:     trybotsFailed,
	},
}

// hasPlusTwo is a [reviews.Score] function that reports whether
// the change has a +2 vote.
func hasPlusTwo(ctx context.Context, ch reviews.Change) (bool, error) {
	return hasCodeReviewVal(ch, 2)
}

// hasPlusOne is a [reviews.Score] function that reports whether
// the change has a +1 vote.
func hasPlusOne(ctx context.Context, ch reviews.Change) (bool, error) {
	return hasCodeReviewVal(ch, 1)
}

// hasCodeReviewVal reports whether ch has a Code-Review label of val.
func hasCodeReviewVal(ch reviews.Change, val int) (bool, error) {
	gc := ch.(goChange).GerritChange
	label := gc.Client.GClient.ChangeLabel(gc.Change, "Code-Review")
	if label == nil {
		return false, nil
	}
	for _, ai := range label.All {
		if ai.Value == val {
			return true, nil
		}
	}
	return false, nil
}

// hasUnresolvedComments is a [reviews.Score] function that reports whether
// the change has unresolved comments.
func hasUnresolvedComments(ctx context.Context, ch reviews.Change) (bool, error) {
	gc := ch.(goChange).GerritChange
	_, unresolved := gc.Client.GClient.ChangeCommentCounts(gc.Change)
	return unresolved > 0, nil
}

// trybotsPassed is a [reviews.Score] function that reports whether
// the trybots passed. We check both the LUCI and non-LUCI trybots for now.
func trybotsPassed(ctx context.Context, ch reviews.Change) (bool, error) {
	return trybotsCheck(ch, 1)
}

// trybotsFailed is a [reviews.Score] function that reports whether
// the trybots failed. We check both the LUCI and non-LUCI trybots for now.
func trybotsFailed(ctx context.Context, ch reviews.Change) (bool, error) {
	return trybotsCheck(ch, -1)
}

// trybotsCheck reports whether the trybot results have the value val.
func trybotsCheck(ch reviews.Change, val int) (bool, error) {
	gc := ch.(goChange).GerritChange

	seen := func(ai *gerrit.ApprovalInfo) bool {
		return ai.Value == val
	}

	label := gc.Client.GClient.ChangeLabel(gc.Change, "LUCI-TryBot-Result")
	if label != nil && slices.ContainsFunc(label.All, seen) {
		return true, nil
	}

	label = gc.Client.GClient.ChangeLabel(gc.Change, "TryBot-Result")
	if label != nil && slices.ContainsFunc(label.All, seen) {
		return true, nil
	}

	return false, nil
}

// rejects is a list of [reviews.Reject] values that are specific to Go.
var rejects = []reviews.Reject{
	{
		Name:        "goreviewable",
		Description: "whether the change is reviewable (Go specific)",
		Applies:     unreviewable,
	},
	{
		Name:        "waitRelease",
		Description: "change is tagged wait-release",
		Applies:     waitRelease,
	},
	{
		Name:        "hold",
		Description: "change is on hold",
		Applies:     hold,
	},
}

// unreviewable is a [reviews.Reject] function that reports that
// a Go change is unreviewable. Putting "DO NOT REVIEW" in the description
// is a Go project convention for marking a change unreviewable.
func unreviewable(ctx context.Context, ch reviews.Change) (bool, error) {
	desc := ch.Description(ctx)
	if strings.Contains(desc, "DO NOT REVIEW") {
		return true, nil
	}
	return false, nil
}

// waitRelease is a [reviews.Reject] function that reports whether
// the change has the wait-release tag.
func waitRelease(ctx context.Context, ch reviews.Change) (bool, error) {
	gc := ch.(goChange).GerritChange
	tags := gc.Client.GClient.ChangeHashtags(gc.Change)
	r := slices.Contains(tags, "wait-release")
	return r, nil
}

// hold is a [reviews.Reject] function that reports whether
// the change is on hold.
func hold(ctx context.Context, ch reviews.Change) (bool, error) {
	gc := ch.(goChange).GerritChange
	label := gc.Client.GClient.ChangeLabel(gc.Change, "Hold")
	if label == nil {
		return false, nil
	}
	for _, ai := range label.All {
		if ai.Value == 1 {
			return true, nil
		}
	}
	return false, nil
}

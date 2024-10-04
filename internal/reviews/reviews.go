// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package reviews contains tools for project maintainers to
// categorize incoming changes, to help them decide what to
// review next.
package reviews

import (
	"time"
)

// A Change is a change suggested for a project.
// This is something like a GitHub pull request or a Gerrit change.
//
// For example, users of a Gerrit repo will read data using
// the gerrit package, and produce a [GerritChange] which implements [Change].
// They can then use the general purpose scoring algorithms to
// categorize the changes.
type Change interface {
	// The change ID, which is unique for a given project.
	// This is something like a Git revision or a Gerrit change number.
	ID() string
	// The change status.
	Status() Status
	// The person or agent who wrote the change.
	Author() Account
	// When the change was created.
	Created() time.Time
	// When the change was last updated.
	Updated() time.Time
	// When the change was last updated by the change author.
	UpdatedByAuthor() time.Time
	// The change subject: the first line of the description.
	Subject() string
	// The complete change description.
	Description() string
	// The list of people whose review is requested.
	Reviewers() []Account
	// The list of people who have reviewed the change.
	Reviewed() []Account
	// What the change needs in order to be submitted.
	Needs() Needs
}

// Status is the status of a change.
type Status int

const (
	// Change is ready for review. The default.
	StatusReady Status = iota
	// Change is submitted.
	StatusSubmitted
	// Change is closed or abandoned.
	StatusClosed
	// Change is open but not ready for review.
	StatusDoNotReview
)

// Needs is a bitmask of missing requirements for a [Change].
// The requirements may not be comprehensive; it's possible that
// when all these requirements are satisfied there will be more.
// If the Needs value is 0 then the change can be submitted.
type Needs int

const (
	// Change needs a review by someone.
	NeedsReview Needs = 1 << iota
	// Change needs to be approved.
	NeedsApproval
	// Change needs maintainer review,
	// but not necessarily approval.
	NeedsMaintainerReview
	// Change needs to resolve a reviewer comment.
	NeedsResolve
	// Change needs to resolve a merge conflict.
	NeedsConflictResolve
	// Change waiting for tests or other checks to pass.
	NeedsCheck
	// Some reviewer has this change on hold,
	// or change is marked as do not submit by author.
	NeedsHoldRemoval
	// Change waiting for next release to open.
	NeedsRelease
	// Change not submittable for some other reason.
	NeedsOther
)

// An Account describes a person or agent.
type Account interface {
	// The unique account name, such as an e-mail address.
	Name() string
	// The display name of the account, such as a person's full name.
	DisplayName() string
	// The authority of this account in the project.
	Authority() Authority
	// Number of commits made by this account to the project.
	Commits() int
}

// AccountLookup looks up account information by name.
// If there is no such account, this returns nil.
// At least for Gerrit, account information is stored
// differently by different Gerrit instances,
// so we need an interface.
type AccountLookup interface {
	Lookup(string) Account
}

// Authority describes what authority a person has in a project.
type Authority int

const (
	// Person is unknown or has no particular status.
	AuthorityUnknown Authority = iota
	// Person has contributed changes.
	AuthorityContributor
	// Person has reviewed changes.
	AuthorityReviewer
	// Person is a maintainer who can review and commit patches by others.
	AuthorityMaintainer
	// Person is a project owner/admin.
	AuthorityOwner
)

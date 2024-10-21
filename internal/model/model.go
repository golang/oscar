// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package model provides a general model for software projects.
// It includes types for content like issues and comments ([Post]), as well
// as types for other aspects of a project, like users and bots ([Identity].)
package model

import (
	"context"
	"iter"
	"time"

	"golang.org/x/oscar/internal/storage/timed"
)

// A Content is a piece of meaningful information.
// It may be a name, an email address, or an entire document.
// TODO: remove trailing underscores after renaming conflicting fields elsewhere.
type Content interface {
	ID() string     // universally unique ID, typically a URL
	Title_() string // empty if missing or not supported
	Body_() string  // text content; some day, a sequence of multimedia elements

	// These are zero if unknown.
	CreatedAt_() time.Time
	UpdatedAt_() time.Time
}

// A Post is a piece of content that can be easily made public ("posted").
// Examples are email messages, blog posts, and issue comments.
//
// A Post may have children.
type Post interface {
	Content

	Project() string   // ID of project this belongs to
	Author() *Identity // nil if unknown or not supported
	CanEdit() bool     // can the content or metadata of this post be modified?

	CanHaveChildren() bool // can this kind of Post have children?
	ParentID() string      // the parent post's ID, empty if this is a root post
}

// An Identity is an entity that can interact with a project.
// It contains a unique identifier for a person, team or bot.
type Identity struct {
	Realm Realm
	ID    string // unique for the realm
	Name  string // non-canonical display name, if known (e.g. "Pat Brown", "The Go Team")
}

// A Realm is a namespace for identity unique IDs.
type Realm string

const (
	// GitHub user/org names.
	GitHub Realm = "GitHub"
	// Gerrit user names.
	Gerrit Realm = "Gerrit"
	// Email addresses.
	Email Realm = "email"
)

// A Source allows direct access to an external system, or a part of a system.
type Source[T Content] interface {
	// A unique name for the source. For example, "GitHubIssues".
	Name() string

	// CRUD operations

	// Download the current value for the ID from the external source.
	Read(ctx context.Context, id string) (T, error)

	// Update c by applying the given changes.
	// Keys of changes are field names. Values of changes are the new values.
	// If changes is empty, Update does nothing and returns nil.
	// Update returns an error if a field doesn't exist or is not modifiable.
	// Update panics if a value in changes is the wrong type for the field.
	Update(ctx context.Context, c T, changes map[string]any) error

	// Create a new instance of T in the source, returning its unique ID.
	Create(context.Context, T) (id string, err error)

	// Delete the item with the given ID.
	// No error is returned if the ID is not found.
	// An error is returned if deletion is not unsupported or the item cannot be deleted.
	Delete(_ context.Context, id string) error
}

// A Watcher is a persistent, named cursor into a database.
// See [timed.Watcher] for an implementation (currently the only one) and for method documentation.
type Watcher[T any] interface {
	Recent() iter.Seq[T]
	Restart()
	MarkOld(timed.DBTime)
	Flush()
	Latest() timed.DBTime
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package discussion implements a sync mechanism to mirror GitHub
// discussions state into a [storage.DB].
// All the functionality is provided by the [Client], created by [New].
//
// This package stores the following key schemas in the database:
//
//	["discussion.SyncProject", Project] => JSON of [projectSync] structure
//	["discussion.Event", Project, Discussion, API, ID] => [DBTime, Raw(JSON)]
//	["discussion.EventByTime", DBTime, Project, Discussion, API, ID] => []
//
// To reconstruct the history of a given discussion, scan for keys from
// ["discussion.Event", Project, Discussion] to ["discussion.Event", Project, Discussion, ordered.Inf].
//
// The API field is "/discussions", or "/discussions/comments",
// so the first key-value pair is the discussion with its body text and
// metadata.
//
// The IDs are GitHub's and appear to be ordered by creation time within an API,
// so that the comments are time-ordered and the discussions are time-ordered,
// but comments and discussions are not ordered with respect to each other.
// To order them fully, fetch all the events and sort by the time in the JSON.
//
// The JSON is the raw JSON served from GitHub describing the event.
// Storing the raw JSON avoids having to re-download everything if we decide
// another field is of interest to us.
//
// EventByTime is an index of Events by DBTime, which is the time when the
// record was added to the database, which is not necessarily related
// to the time the event occurred. Code that processes new events can
// record which DBTime it has most recently processed and then scan forward in
// the index to learn about new events.
package discussion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

// Client is a client for making requests to the
// GitHub GraphQL API and syncing discussion state with
// a [storage.DB].
type Client struct {
	gql *gqlClient

	slog *slog.Logger
	db   storage.DB
}

// New creates a new client for making requests to the GitHub
// GraphQL API.
//
// The secret database is expected to have a secret named "api.github.com" of the
// form "user:pass" where user is a user-name (ignored by GitHub) and pass is an API token
// ("ghp_...").
func New(ctx context.Context, lg *slog.Logger, sdb secret.DB, db storage.DB) *Client {
	return &Client{
		gql:  newGQLClient(authClient(ctx, sdb)),
		slog: lg,
		db:   db,
	}
}

// Sync syncs all projects.
func (c *Client) Sync(ctx context.Context) error {
	var errs []error
	for key := range c.db.Scan(o(syncProjectKind), o(syncProjectKind, ordered.Inf)) {
		var project string
		if err := ordered.Decode(key, nil, &project); err != nil {
			c.db.Panic("discussion.Client sync decode", "key", storage.Fmt(key), "err", err)
		}
		if err := c.SyncProject(ctx, project); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// SyncProject syncs a single project.
func (c *Client) SyncProject(ctx context.Context, project string) error {
	key := key(project)
	skey := string(key)

	// Lock the project, so that no else is sync'ing concurrently.
	c.db.Lock(skey)
	defer c.db.Unlock(skey)

	// Load sync state.
	proj, ok, err := load(c.db, project)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("discussion.SyncProject: unknown project: %q", project)
	}

	if err := c.syncByDate(ctx, proj, discussionAPI); err != nil {
		return err
	}
	return c.syncByDate(ctx, proj, commentAPI)
}

// discussionEventsSince returns an iterator over the discussion events since the given time.
func (c *gqlClient) discussionEventsSince(ctx context.Context, proj *projectSync, since time.Time) iter.Seq2[*Event, error] {
	return func(yield func(*Event, error) bool) {
		for disc, err := range c.discussions(ctx, proj.Owner, proj.Repo) {
			if err != nil {
				yield(nil, err)
				return
			}

			e, err := disc.toEvent(proj.Project)
			if err != nil {
				yield(nil, err)
				return
			}

			if e.Updated.After(since) {
				if !yield(e, nil) {
					return
				}
				continue
			}

			// Discussions are ordered by update time so we can break early
			// if we find a discussion updated at or before since.
			return
		}
	}
}

// commentEventsSince returns an iterator over the discussion events since the given time.
func (c *gqlClient) commentEventsSince(ctx context.Context, proj *projectSync, since time.Time) iter.Seq2[*Event, error] {
	return func(yield func(*Event, error) bool) {
		for c, err := range c.comments(ctx, proj.Owner, proj.Repo) {
			if err != nil {
				yield(nil, err)
				return
			}

			e, err := c.toEvent(proj.Project)
			if err != nil {
				yield(nil, err)
				return
			}

			// Comments don't have a guaranteed order, so never break early.
			if e.Updated.After(since) {
				if !yield(e, nil) {
					return
				}
			}
		}
	}
}

// syncByDate syncs the events for a given project and api.
// It records all new events since the appropriate date (proj.DisscussionDate
// or proj.CommentDate, depending on the API).
// If successful, it updates the date to the latest event date seen.
// TODO(tatianabradley): Add partial progress markers, in case a sync is so
// big that it hits a timeout.
func (c *Client) syncByDate(ctx context.Context, proj *projectSync, api string) error {
	var sinceStr *string
	var eventsSince func(context.Context, *projectSync, time.Time) iter.Seq2[*Event, error]
	switch api {
	case discussionAPI:
		sinceStr = &proj.DiscussionDate
		eventsSince = c.gql.discussionEventsSince
	case commentAPI:
		sinceStr = &proj.CommentDate
		eventsSince = c.gql.commentEventsSince
	default:
		// unreachable except bug in this package
		c.db.Panic("unrecognized api", api)
	}
	since, err := parseTime(*sinceStr)
	if err != nil {
		return err
	}
	latest := time.Time{}

	c.slog.Debug("syncing", "api", api, "project", proj.Project, "after", since)

	b := c.db.Batch()
	defer b.Apply()

	for e, err := range eventsSince(ctx, proj, since) {
		if err != nil {
			return err
		}

		c.slog.Debug("syncing discussion event", "project", proj.Project,
			"discussion", e.Discussion, "api", e.API,
			"updated", e.Updated, "value", e.Typed)

		c.writeEvent(b, e)
		b.MaybeApply()

		if e.Updated.After(latest) {
			latest = e.Updated
		}
	}

	b.Apply()
	*sinceStr = latest.Format(time.RFC3339)
	proj.store(c.db)

	return nil
}

// toEvent converts a Discussion to an Event.
func (d *Discussion) toEvent(project string) (*Event, error) {
	updated, err := parseTime(d.UpdatedAt)
	if err != nil {
		return nil, err
	}

	return &Event{
		Project:    project,
		Discussion: d.Number,
		API:        discussionAPI,
		ID:         d.Number,
		JSON:       storage.JSON(d),
		Updated:    updated,
	}, nil
}

// parseTime converts a string to a time in [time.RFC3339] format.
func parseTime(t string) (time.Time, error) {
	if t == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, t)
}

// toEvent converts a Comment to an Event.
func (c *Comment) toEvent(project string) (*Event, error) {
	updated, err := parseTime(c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	disc, err := parseID(c.DiscussionURL, "/")
	if err != nil {
		return nil, err
	}
	return &Event{
		Project:    project,
		Discussion: disc,
		API:        commentAPI,
		ID:         c.ID(),
		JSON:       storage.JSON(c),
		Updated:    updated,
	}, nil
}

// parseID returns the numerical ID at the end of the URL, right
// after the last instance of sep.
// e.g. parseID("https://example.com/comment/123","/") returns 123.
// An error is returned if the URL is malformed.
func parseID(u string, sep string) (int64, error) {
	return strconv.ParseInt(u[strings.LastIndex(u, sep)+1:], 10, 64)
}

// writeEvent writes a single event to the database using [timed.Set],
// to maintain a time-ordered index.
func (c *Client) writeEvent(b storage.Batch, e *Event) {
	timed.Set(c.db, b, eventKind, e.key(), o(ordered.Raw(e.JSON)))
}

// key returns the db key for an event.
func (e *Event) key() []byte {
	return o(e.Project, e.Discussion, e.API, e.ID)
}

// Add adds a GitHub project of the form
// "owner/repo" (for example "golang/go")
// to the database.
// It only adds the project sync metadata.
// The initial data fetch does not happen until [Sync] or [SyncProject] is called.
// Add returns an error if the project has already been added.
func (c *Client) Add(project string) error {
	if _, ok, _ := load(c.db, project); ok {
		return fmt.Errorf("discussion.Add: already added: %q", project)
	}
	owner, repo, err := splitProject(project)
	if err != nil {
		return err
	}
	proj := &projectSync{
		Project: project,
		Owner:   owner,
		Repo:    repo,
	}
	proj.store(c.db)
	return nil
}

// store stores proj into db.
func (proj *projectSync) store(db storage.DB) {
	db.Set(key(proj.Project), storage.JSON(proj))
}

// load retrieves a project from the db and reports whether
// the project is found.
// It returns an error if the project exists but cannot be unmarshaled.
func load(db storage.DB, project string) (_ *projectSync, ok bool, _ error) {
	key := key(project)
	p, ok := db.Get(key)
	if !ok {
		return nil, false, nil
	}
	var proj projectSync
	if err := json.Unmarshal(p, &proj); err != nil {
		return nil, true, fmt.Errorf("discussions.load: cannot unmarshal sync for project %s: %w", project, err)
	}
	return &proj, true, nil
}

// key returns the db key for a discussion project sync.
func key(project string) []byte {
	return o(syncProjectKind, project)
}

// Kinds of database entries.
const (
	syncProjectKind = "discussion.SyncProject"
	eventKind       = "discussion.Event"
)

// o is short for ordered.Encode.
func o(list ...any) []byte { return ordered.Encode(list...) }

// splitProject returns the owner and repo for a project of the form
// "owner/repo".
func splitProject(project string) (owner, repo string, _ error) {
	parts := strings.Split(project, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid project: %s", project)
	}
	return parts[0], parts[1], nil
}

// projectSync is the state of a discussions sync in the DB.
type projectSync struct {
	Project        string // owner/repo
	Owner, Repo    string
	DiscussionDate string // updated time of last synced discussion
	CommentDate    string // updated time of last synced comment/reply
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package discussion implements a sync mechanism to mirror GitHub
// discussions state into a [storage.DB].
// All the functionality is provided by the [Client], created by [New].
package discussion

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
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
func New(ctx context.Context, lg *slog.Logger, sdb secret.DB, db storage.DB) *Client {
	return &Client{
		gql:  newGQLClient(authClient(ctx, sdb)),
		slog: lg,
		db:   db,
	}
}

// TODO(tatianabradley): Implement Sync.
func (c *Client) Sync(ctx context.Context, project string) error {
	_, _, err := splitProject(project)
	if err != nil {
		return err
	}
	return errors.New("not implemented")
}

// splitProject returns the owner and repo for a project of the form
// "owner/repo".
func splitProject(project string) (owner, repo string, _ error) {
	parts := strings.Split(project, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid project: %s", project)
	}
	return parts[0], parts[1], nil
}

// projectState is the state of a discussions sync in the DB.
type projectState struct {
	Project        string // owner/repo
	DiscussionDate string // last successful sync of discussions
	CommentDate    string // last successful sync of comments and replies
}

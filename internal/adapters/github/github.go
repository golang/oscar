// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package github is an adapter for GitHub.
// It consists of [model.Content] and [model.Source] implementations for
// issues and discussions.
package github

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"

	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
)

// An Adapter is a connection to GitHub state in a database and on GitHub itself.
type Adapter struct {
	ic *github.Client // for issues
}

// New returns a new adapter that uses the given logger, databases, and HTTP client.
//
// The secret database is expected to have a secret named "api.github.com" of the
// form "user:pass" where user is a user-name (ignored by GitHub) and pass is an API token
// ("ghp_...").
func New(lg *slog.Logger, db storage.DB, sdb secret.DB, hc *http.Client) *Adapter {
	return &Adapter{
		ic: github.New(lg, db, sdb, hc),
	}
}

// Add adds a GitHub project of the form
// "owner/repo" (for example "golang/go")
// to the database.
// It only adds the project sync metadata.
// The initial data fetch does not happen until [Sync] or [SyncProject] is called.
// Add returns an error if the project has already been added.
func (a *Adapter) AddProject(project string) error {
	return cmp.Or(a.ic.Add(project), nil)
	// TODO: add discussion sync as second arg to cmp.Or.
}

// Sync syncs all projects.
func (a *Adapter) Sync(ctx context.Context) error {
	return cmp.Or(a.ic.Sync(ctx), nil)
	// TODO: add discussion sync as second arg to cmp.Or.
}

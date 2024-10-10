// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package goreviews collects Go CLs for a dashboard for the Go project.
package goreviews

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"golang.org/x/oscar/internal/gerrit"
	"golang.org/x/oscar/internal/reviews"
)

// collectChanges collects all the changes for the given projects,
// converts them to [reviews.Change] values, and scores them.
func collectChanges(ctx context.Context, lg *slog.Logger, client *gerrit.Client, projects []string) ([]reviews.ChangePreds, error) {
	lg.Info("gathering change information")
	start := time.Now()

	accounts := &accountsData{
		data: make(map[string]*accountData),
	}

	chAccount := make(chan *gerrit.Change, 16)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		collectAccounts(client, accounts, chAccount)
	}()

	var changes []*gerrit.Change
	for _, project := range projects {
		for _, changeFn := range client.ChangeNumbers(project) {
			gchange := changeFn()

			changes = append(changes, gchange)

			select {
			case chAccount <- gchange:
			case <-ctx.Done():
				close(chAccount)
				wg.Wait()
				return nil, ctx.Err()
			}
		}
	}

	close(chAccount)
	wg.Wait()

	// We have all the account data, so we can now apply predicates.

	// Convert to reviews.GerritChange, which implements review.Change.
	// We wrap GerritChange ourselves to reimplement the Author method.
	// Create an iterator we can pass to reviews.CollectChangePreds.
	toReviews := func(yield func(reviews.Change) bool) {
		for gch := range reviews.GerritChanges(client, accounts, slices.Values(changes)) {
			if !yield(goChange{gch}) {
				return
			}
		}
	}

	cps := reviews.CollectChangePreds(ctx, lg, toReviews)

	lg.Info("finished gathering change information", "duration", time.Since(start))

	return cps, nil
}

// goChange implements [gerrit.Change], with some customizations
// appropriate for the Go project.
type goChange struct {
	*reviews.GerritChange
}

const gerritbotEmail = "letsusegerrit@gmail.com"

// Author returns the change author.
// For changes copied from GitHub we switch from GerritBot
// to the GitHub author.
func (gc goChange) Author() reviews.Account {
	owner := gc.GerritChange.Author().Name()
	if owner == gerritbotEmail {
		gpi := githubOwner(gc.Client.GClient, gc.Change)
		if gpi != nil {
			owner = gpi.Email
		}
	}
	return gc.Client.Accounts.Lookup(owner)
}

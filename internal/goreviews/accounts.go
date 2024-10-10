// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package goreviews

import (
	"runtime"
	"sync"

	"golang.org/x/oscar/internal/gerrit"
	"golang.org/x/oscar/internal/reviews"
)

// accountsData holds information about Gerrit accounts that we care about.
type accountsData struct {
	mu   sync.Mutex
	data map[string]*accountData // map from account email to account data
}

// accountData is the information we track for an account.
// This implements [reviews.Account].
type accountData struct {
	name        string // unique e-mail address for account
	displayName string // full user name
	commits     int    // number of commits written by this account
	reviews     int    // number of reviews by this account
	canSubmit   bool   // whether this account has submitted changes
	canPlusTwo  bool   // whether this account has +2'ed changes
}

// collectAccounts collects information from a sequence of changes.
func collectAccounts(client *gerrit.Client, asd *accountsData, ch <-chan *gerrit.Change) {
	count := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	wg.Add(count)
	for range count {
		go func() {
			defer wg.Done()
			collectAccountsWorker(client, asd, ch)
		}()
	}

	wg.Wait()
}

// collectAccountsWorker collects account information for changes read from ch.
func collectAccountsWorker(client *gerrit.Client, asd *accountsData, ch <-chan *gerrit.Change) {
	// Collect data in a local map, and then transfer to asd.
	a := make(map[string]*accountData)
	alc := func(ai *gerrit.AccountInfo) {
		name := ai.Email
		if a[name] == nil {
			var displayName string
			switch {
			case ai.DisplayName != "":
				displayName = ai.DisplayName
			case ai.Name != "":
				displayName = ai.Name
			default:
				displayName = name
			}

			a[name] = &accountData{
				name:        name,
				displayName: displayName,
			}
		}
	}

	for change := range ch {
		ownerAccount := client.ChangeOwner(change)
		alc(ownerAccount)
		owner := ownerAccount.Email
		if owner == gerritbotEmail {
			gpi := githubOwner(client, change)
			if gpi != nil {
				owner = gpi.Email
				if a[owner] == nil {
					a[owner] = &accountData{
						name:        gpi.Email,
						displayName: gpi.Name,
					}
				}
			}
		}

		if client.ChangeStatus(change) == "MERGED" {
			a[owner].commits++

			submitter := client.ChangeSubmitter(change)
			// Some changes in the Go repo have no submitter.
			// For example, CL 4320.
			if submitter != nil {
				alc(submitter)
				a[submitter.Email].canSubmit = true
			}
		}

		// Find the list of accounts that reviewed the change.
		// We consider any account that sent a message on the change
		// as reviewing the change.
		msgs := client.ChangeMessages(change)
		for _, msg := range msgs {
			if msg.RealAuthor != nil {
				alc(msg.RealAuthor)
				if msg.RealAuthor.Email != owner {
					a[msg.RealAuthor.Email].reviews++
				}
			} else if msg.Author != nil {
				alc(msg.Author)
				if msg.Author.Email != owner {
					a[msg.Author.Email].reviews++
				}
			}
		}
		project := client.ChangeProject(change)
		for _, comments := range client.Comments(project, client.ChangeNumber(change)) {
			for _, comment := range comments {
				if comment.Author != nil {
					alc(comment.Author)
					if comment.Author.Email != owner {
						a[comment.Author.Email].reviews++
					}
				}
			}
		}

		// Find the list of accounts that voted Code-Review +2.
		label := client.ChangeLabel(change, "Code-Review")
		if label != nil {
			for _, ai := range label.All {
				if ai.Value == 2 {
					alc(ai.AccountInfo)
					a[ai.Email].canPlusTwo = true
				}
			}
		}
	}

	if len(a) == 0 {
		return
	}

	asd.mu.Lock()
	defer asd.mu.Unlock()

	for name, xad := range a {
		pad := asd.data[name]
		if pad == nil {
			pad = &accountData{
				name:        name,
				displayName: xad.displayName,
			}
			asd.data[name] = pad
		}

		pad.commits += xad.commits
		pad.reviews += xad.reviews
		if xad.canSubmit {
			pad.canSubmit = true
		}
		if xad.canPlusTwo {
			pad.canPlusTwo = true
		}
	}
}

// Lookup looks up an account.
// This implements [reviews.AccountLookup].
func (asd *accountsData) Lookup(name string) reviews.Account {
	ad := asd.data[name]
	if ad == nil {
		// We never saw this account, which will be the case for
		// accounts that never submitted a change and never voted.
		// Return a nil pointer, which we handle in the method
		// implementations below.
		return (*accountData)(nil)
	}
	return ad
}

// Name returns the account name.
func (ad *accountData) Name() string {
	if ad == nil {
		return "unknown"
	}
	return ad.name
}

// DisplayName returns the display name.
func (ad *accountData) DisplayName() string {
	if ad == nil {
		return "unknown"
	}
	return ad.displayName
}

// Authority returns the authority of the account in the project.
func (ad *accountData) Authority() reviews.Authority {
	if ad == nil {
		return reviews.AuthorityUnknown
	}

	// We never return AuthorityOwner.
	// For the Go project I think we could only get that
	// by looking at group membership.

	switch {
	case ad.canPlusTwo || ad.canSubmit:
		return reviews.AuthorityMaintainer
	case ad.reviews > 0:
		return reviews.AuthorityReviewer
	case ad.commits > 0:
		return reviews.AuthorityContributor
	default:
		return reviews.AuthorityUnknown
	}
}

// Commits returns the number of commits made the account.
func (ad *accountData) Commits() int {
	if ad == nil {
		return 0
	}
	return ad.commits
}

// githubOwner finds the owner of a change that was created by [GerritBot]
// from a GitHub pull request. This returns nil if the information can't
// be retrieved.
//
// [GerritBot]: https://go.dev/wiki/GerritBot
func githubOwner(client *gerrit.Client, change *gerrit.Change) *gerrit.GitPersonInfo {
	return client.ChangeCommitAuthor(change, 1)
}

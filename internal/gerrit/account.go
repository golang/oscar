// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gerrit

import (
	"encoding/json"
	"sync"
)

// accountCache is a cache of accounts we read from a Gerrit instance.
// It maintains a mapping from account ID to account information so
// that we can easily compare accounts for equality and so that we don't
// don'get another copy of the account information every time we unmarshal.
type accountCache struct {
	mu       sync.Mutex
	accounts map[int]*AccountInfo
}

// loadAccount unmarshals the JSON for a single Gerrit account.
// It returns a canonical AccountInfo.
// This may be called concurrently by multiple goroutines.
func (c *Client) loadAccount(accountJSON json.RawMessage) *AccountInfo {
	if len(accountJSON) == 0 {
		return nil
	}

	var id struct {
		AccountID int `json:"_account_id"`
	}
	if err := json.Unmarshal(accountJSON, &id); err != nil {
		c.slog.Error("gerrit account ID decode failure", "data", accountJSON, "err", err)
		c.db.Panic("gerrit account ID decode failure: %v", err)
	}

	ac := &c.ac
	ac.mu.Lock()
	ai, ok := ac.accounts[id.AccountID]
	ac.mu.Unlock()

	if ok {
		return ai
	}

	if err := json.Unmarshal(accountJSON, &ai); err != nil {
		c.slog.Error("gerrit account decode failure", "num", id.AccountID, "data", accountJSON, "err", err)
		c.db.Panic("gerrit account decode failure for %d: %v", id.AccountID, err)
	}

	// Use a function literal so we can defer the unlock.
	update := func() *AccountInfo {
		ac.mu.Lock()
		defer ac.mu.Unlock()

		aiNew, ok := ac.accounts[id.AccountID]
		if ok {
			// Somebody else already cached this account.
			return aiNew
		}

		if ac.accounts == nil {
			ac.accounts = make(map[int]*AccountInfo)
		}

		ac.accounts[id.AccountID] = ai
		return ai
	}

	return update()
}

// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gerrit

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"golang.org/x/oscar/internal/storage"
)

// mergeCacheDuration is how long we cache mergeability information.
//
// Whether a change can be merged is a property of both the change
// and the repo. That is, we can't just recompute it when there is
// activity on a change; it may change due to other activity on the repo.
// To avoid clobbering the Gerrit server, we cache the information.
// This means that the information can be out of date.
// mergeCacheDuration sets a limit on how out of date we let it get.
const mergeCacheDuration = 72 * time.Hour

// mergeCacheTimeType holds the last time we cached merge information
// for any project.
type mergeCacheTimeType struct {
	mu   sync.Mutex // protects when field
	when time.Time
}

// get returns the last time we cached merge information for any project.
// If we have never cached it during this program execution,
// this returns the zero Time.
func (mct *mergeCacheTimeType) get() time.Time {
	mct.mu.Lock()
	defer mct.mu.Unlock()
	return mct.when
}

// set sets the last time we cached merge information for any project.
// This only updates the time if it is newer than the current cache time.
func (mct *mergeCacheTimeType) set(tm time.Time) {
	mct.mu.Lock()
	defer mct.mu.Unlock()
	if mct.when.IsZero() || mct.when.Before(tm) {
		mct.when = tm
	}
}

// mergeCacheTime is the last time we cached merge information
// for any project.
var mergeCacheTime mergeCacheTimeType

// ChangeMergeable returns whether a change is mergeable.
// If we don't know, it returns true as the safe default.
// The result may be out of date, as it is expensive to compute.
//
// This takes a Context because it may start a background
// goroutine to compute change mergeability; the Context
// will be used for that goroutine.
func (c *Client) ChangeMergeable(ctx context.Context, ch *Change) bool {
	if c.divertChanges() {
		return c.testClient.isMergeable(c.ChangeNumber(ch))
	}

	c.computeMergeable(ctx)

	changeNum := c.ChangeNumber(ch)
	project := c.ChangeProject(ch)
	key := o(changeMergeableKind, c.instance, project, changeNum)
	val, ok := c.db.Get(key)
	if !ok {
		// We have no information about this change;
		// default to being mergeable.
		return true
	}

	var mergeable bool
	if err := json.Unmarshal(val, &mergeable); err != nil {
		c.db.Panic("mergeable unmarshal failed", "change", changeNum, "val", val, "err", err)
	}
	return mergeable
}

// computeMergeable starts a goroutine to recompute mergeability
// for all open changes. The goroutine is only started if it has
// been long enough since the last time we ran the goroutine.
func (c *Client) computeMergeable(ctx context.Context) {
	when := mergeCacheTime.get()
	if !when.IsZero() && time.Since(when) < mergeCacheDuration {
		return
	}

	go func() {
		var lastCheck time.Time
		for project := range c.projects() {
			projCheck := c.computeMergeableProject(ctx, project)
			if lastCheck.IsZero() || projCheck.Before(lastCheck) {
				lastCheck = projCheck
			}
		}

		mergeCacheTime.set(lastCheck)
	}()
}

// computeMergeableProject recomputes mergeability for the
// changes in a project. It returns the last time the project
// mergeability information was updated.
func (c *Client) computeMergeableProject(ctx context.Context, project string) time.Time {
	// Only let one instance recompute mergeability.
	key := o(changeMergeableKind, c.instance, project)
	skey := string(key)
	c.db.Lock(skey)
	defer c.db.Unlock(skey)

	// Only recompute mergeability if it is out of date.
	if val, ok := c.db.Get(key); ok {
		var lastUpdate time.Time
		if err := json.Unmarshal(val, &lastUpdate); err != nil {
			c.db.Panic("gerrit changeMergeable decode", "key", storage.Fmt(key), "val", storage.Fmt(val), "err", err)
		}

		if time.Since(lastUpdate) < mergeCacheDuration {
			return lastUpdate
		}
	}

	b := c.db.Batch()
	defer func() {
		b.Apply()
		c.db.Flush()
	}()

	for changeNum, changeFn := range c.ChangeNumbers(project) {
		if c.ChangeStatus(changeFn()) != "NEW" {
			continue
		}
		c.computeMergeableChange(ctx, b, project, changeNum)
	}

	now := time.Now()
	b.Set(key, storage.JSON(now))
	return now
}

// computeMergeableChange recomputes mergeability for a single change.
// This contacts Gerrit to get the current status.
func (c *Client) computeMergeableChange(ctx context.Context, b storage.Batch, project string, changeNum int) {
	var mergeable struct {
		Mergeable bool `json:"mergeable"`
	}

	url := "https://" + c.instance + "/changes/" + strconv.Itoa(changeNum) + "/revisions/current/mergeable"
	if err := c.get(ctx, url, &mergeable); err != nil {
		c.slog.Error("mergeable fetch failed", "change", changeNum, "err", err)
		return
	}

	key := o(changeMergeableKind, c.instance, project, changeNum)
	b.Set(key, storage.JSON(mergeable.Mergeable))
	b.MaybeApply()
}

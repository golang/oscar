// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gerrit mirrors Gerrit CL state in a [storage.DB].
package gerrit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

const (
	syncProjectKind  = "gerrit.SyncProject"
	changeKind       = "gerrit.Change"
	commentKind      = "gerrit.Comment"
	changeUpdateKind = "gerrit.ChangeUpdate"
)

// For a Gerrit project we store changes indexed by change number.
// For each change we store the latest known state, as the JSON
// encoding of what Gerrit calls a ChangeInfo entity.
// We also store comments on the change, a JSON array of Gerrit
// CommentInfo entities.
//
// We store a simple timed stream of updated change numbers.
// We don't store historical status of changes, as all historical
// information is stored in the ChangeInfo and CommentInto entities.
//
// The following key schemas are stored in the database:
//
//	["gerritSyncProject", Instance, Project] => JSON of projectSync structure
//	["gerrit.Change", Instance, Project, ChangeNumber] => ChangeInfo JSON
//	["gerrit.Comment", Instance, Project, ChangeNumber] => CommentInfo JSON
//	["gerrit.ChangeUpdate", Instance, ChangeNumber, MetaID] => DBTime
//	["gerrit.ChangeUpdateByTime", DBTime, Instance, ChangeNumber, MetaID] => []
//
// A watcher on "gerrit.ChangeUpdate" will see all Gerrit changes,
// and can read the new data from the database.

// changeOpts is the options we request for a change.
var changeOpts = []string{
	"ALL_REVISIONS",
	"DETAILED_ACCOUNTS",
	"LABELS",
	"ALL_COMMITS",
	"MESSAGES",
	"SUBMITTABLE",
	"PARENTS",
}

// o is short for ordered.Encode.
func o(list ...any) []byte { return ordered.Encode(list...) }

// A Client is a connection to a Gerrit instance, and to the database
// that stores information gathered from the instance.
type Client struct {
	instance string
	slog     *slog.Logger
	db       storage.DB
	secret   secret.DB
	http     *http.Client

	flushRequested atomic.Bool // flush database to disk when convenient

	testing bool
}

// New returns a new client to access a Gerrit instance
// described by a host name like "go-review.googlesource.com".
// The client uses the given logger, databases, and HTTP client.
//
// The secret database will look for a secret whose name is the
// Gerrit instance. The value will be user:pass.  This is not yet used.
func New(instance string, lg *slog.Logger, db storage.DB, sdb secret.DB, hc *http.Client) *Client {
	return &Client{
		instance: instance,
		slog:     lg,
		db:       db,
		secret:   sdb,
		http:     hc,
		testing:  testing.Testing(),
	}
}

// RequestFlush asks a Gerrit sync to flush the database to disk
// when convenient. This may be called concurrently with Sync.
func (c *Client) RequestFlush() {
	c.flushRequested.Store(true)
}

// projectSync records the sync state of a Gerrit project within
// an instance, such as "go" or "website".
// This is stored in the database.
type projectSync struct {
	Instance string // instance host name, "go-review.googlesource.com"
	Name     string // project name, such as "go" or "oscar".
	SyncDate string // date/time of last sync
}

// store stores inst into db.
func (proj *projectSync) store(db storage.DB) {
	db.Set(o(syncProjectKind, proj.Instance, proj.Name), storage.JSON(proj))
}

// Add adds a Gerrit project such as "go" or "oscar" to the database.
// It only adds the project sync metadata.
// The initial data fetch does not happen until [Sync] or [SyncProject]
// is called.
// Add returns an error if the project has already been added.
func (c *Client) Add(project string) error {
	key := o(syncProjectKind, c.instance, project)
	if _, ok := c.db.Get(key); ok {
		return fmt.Errorf("gerrit.Add: already added: %q", project)
	}
	proj := &projectSync{
		Instance: c.instance,
		Name:     project,
	}
	c.db.Set(key, storage.JSON(proj))
	return nil
}

// Sync syncs the data for all projects in this client's instance.
func (c *Client) Sync(ctx context.Context) error {
	var errs []error
	for key := range c.db.Scan(o(syncProjectKind, c.instance), o(syncProjectKind, c.instance, ordered.Inf)) {
		var project string
		if err := ordered.Decode(key, nil, nil, &project); err != nil {
			c.db.Panic("gerrit client sync decode", "key", storage.Fmt(key), "err", err)
		}
		if err := c.SyncProject(ctx, project); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// SyncProject syncs a single project.
func (c *Client) SyncProject(ctx context.Context, project string) (err error) {
	c.slog.Debug("gerrit.SyncProject", "project", project)
	defer func() {
		if err != nil {
			err = fmt.Errorf("SyncProject(%q): %w", project, err)
		}
	}()

	key := o(syncProjectKind, c.instance, project)
	skey := string(key)

	// Lock the project, so that no else is sync'ing concurrently.
	c.db.Lock(skey)
	defer c.db.Unlock(skey)

	// Load sync state.
	var proj projectSync
	if val, ok := c.db.Get(key); !ok {
		return errors.New("missing project")
	} else if err := json.Unmarshal(val, &proj); err != nil {
		return err
	}

	return c.syncChanges(ctx, &proj)
}

// syncChanges finds all changes that have been updated since the last
// sync time. It stores the data for those changes in the database.
// It also adds in the metadata changes.
func (c *Client) syncChanges(ctx context.Context, proj *projectSync) (err error) {
	b := c.db.Batch()
	defer b.Apply()

	// When we need to fetch multiple lists of changes,
	// concurrent modifications can cause us to see the
	// same change more than once. Keep track of the changes
	// we've already seen.
	seen := make(map[int]json.RawMessage)

	// We see the changes from newest to oldest.
	// Update the sync time if we get through
	// all of them with no error.
	lastUpdate := ""
	defer func() {
		if err == nil && lastUpdate != "" {
			proj.SyncDate = lastUpdate
			proj.store(c.db)
		}
	}()

	for change, err := range c.changes(ctx, proj.Name, proj.SyncDate) {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		if c.flushRequested.Load() {
			// Flush database.
			b.Apply()
			c.db.Flush()
			c.flushRequested.Store(false)
		}

		if lastUpdate == "" {
			// Save the update time of the most recent
			// change to record in the project sync.
			var updated struct {
				Updated string `json:"updated"`
			}
			if err := json.Unmarshal(change, &updated); err != nil {
				return err
			}
			lastUpdate = updated.Updated
		}

		// Change is a Gerrit ChangeInfo in JSON form.
		// Pull out the change number.
		var num struct {
			Number int    `json:"_number"`
			MetaID string `json:"meta_rev_id"`
		}
		if err := json.Unmarshal(change, &num); err != nil {
			return err
		}
		changeNum := num.Number
		metaID := num.MetaID
		if changeNum == 0 {
			return fmt.Errorf("missing _number field in %q", change)
		}
		if metaID == "" {
			return fmt.Errorf("missing meta_rev_id field in change %d: %q", changeNum, change)
		}

		if oldChange, ok := seen[changeNum]; ok {
			same, err := sameChangeInfo(change, metaID, oldChange)
			if err != nil {
				return err
			}
			if same {
				// Nothing has changed.
				continue
			}
		}

		seen[changeNum] = change

		key := o(changeKind, c.instance, proj.Name, changeNum)
		if oldChange, ok := c.db.Get(key); ok {
			same, err := sameChangeInfo(change, metaID, oldChange)
			if err != nil {
				return err
			}
			if same {
				// Nothing has changed.
				continue
			}
		}

		b.Set(key, change)
		if err := c.syncComments(ctx, b, proj.Name, changeNum); err != nil {
			return err
		}

		// Record that the change was updated.
		timed.Set(c.db, b, changeUpdateKind, o(c.instance, changeNum, metaID), nil)

		b.MaybeApply()
	}

	return nil
}

// syncComments updates the comments of a change in the database.
func (c *Client) syncComments(ctx context.Context, b storage.Batch, project string, changeNum int) error {
	url := "https://" + c.instance + "/changes/" + strconv.Itoa(changeNum) + "/comments"
	var obj json.RawMessage
	if err := c.get(ctx, url, &obj); err != nil {
		return err
	}

	key := o(commentKind, c.instance, project, changeNum)
	b.Set(key, obj)
	return nil
}

// testBefore exists for testing purposes, to only pull changes before
// some time. For ordinary use this should be the empty string.
var testBefore string

// changes returns an iterator over changes in the Gerrit repo.
// If date is not the empty string, changes only returns changes
// that were updated after date.
func (c *Client) changes(ctx context.Context, project, date string) iter.Seq2[json.RawMessage, error] {
	return func(yield func(json.RawMessage, error) bool) {
		baseURL := "https://" + c.instance + "/changes"

		// Gerrit returns up to 500 changes at a time.
		// Gerrit provides a way to skip leading changes,
		// by passing an S parameter.
		// Unfortunately, S only works up to a value of 10,000.
		// Beyond that Gerrit returns no changes.
		// So to get all the changes we have to ask for changes
		// before some time.
		before := testBefore

		for {
			values := url.Values{
				"o": changeOpts,
			}
			query := "p:" + project
			if date != "" {
				query += " after:" + date
			}
			if before != "" {
				query += " before:" + before
			}
			values.Set("q", query)
			addr := baseURL + "?" + values.Encode()

			var body []json.RawMessage
			if err := c.get(ctx, addr, &body); err != nil {
				yield(nil, err)
				return
			}

			if len(body) == 0 {
				return
			}

			// If the last element in body has a
			// _more_changes field, then skip it this time,
			// and pick it up next time with a before query.
			var more struct {
				MoreChanges bool   `json:"_more_changes"`
				Updated     string `json:"updated"`
			}
			last := body[len(body)-1]
			if err := json.Unmarshal(last, &more); err != nil {
				yield(nil, err)
				return
			}
			if more.MoreChanges {
				if len(body) == 1 {
					yield(nil, errors.New("single change has _more_changes set"))
				}
				body = body[:len(body)-1]
			}

			for _, change := range body {
				if !yield(change, nil) {
					return
				}
			}

			if !more.MoreChanges {
				return
			}

			// Fortunately the before query is inclusive.
			before = more.Updated
		}
	}
}

// sameChangeInfo reports whether two ChangeInfo structures,
// in JSON form, are the same. aMetaID is the meta ID of a.
func sameChangeInfo(a []byte, aMetaID string, b []byte) (bool, error) {
	if bytes.Equal(a, b) {
		return true, nil
	}

	// Unfortunately Gerrit does not return identical ChangeInfo
	// information with consistent field ordering.
	// In particular, we've seen that the order of "reviewers"
	// can change. So we check the meta ID.
	var extractMetaID struct {
		MetaID string `json:"meta_rev_id"`
	}

	if err := json.Unmarshal(b, &extractMetaID); err != nil {
		return false, err
	}
	bMetaID := extractMetaID.MetaID
	if bMetaID == "" {
		return false, errors.New("missing meta ID")
	}

	return aMetaID == bMetaID, nil
}

// get fetches addr and decodes the body as JSON into obj.
func (c *Client) get(ctx context.Context, addr string, obj any) error {
	c.slog.Info("gerrit GET", "addr", addr)

	tries := 0
	backoff := 1 * time.Second
	for {
		req, err := http.NewRequestWithContext(ctx, "GET", addr, nil)
		if err != nil {
			return err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}

		if resp.StatusCode != http.StatusOK {
			data, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				return fmt.Errorf("reading body: %v", err)
			}

			if resp.StatusCode == http.StatusTooManyRequests {
				tries++
				if tries > 20 {
					return errors.New("too many requests")
				}
				c.slog.Info("gerrit too many requests",
					"try", tries,
					"sleep", backoff,
					"body", string(data))
				time.Sleep(backoff)
				backoff = min(backoff*2, 1*time.Minute)

				continue
			}

			return fmt.Errorf("%s\n%s", resp.Status, data)
		}

		// Skip the XSRF header at the start of the response.
		buf := bufio.NewReader(resp.Body)
		defer resp.Body.Close()
		if _, err := buf.ReadSlice('\n'); err != nil {
			return err
		}

		return json.NewDecoder(buf).Decode(obj)
	}
}

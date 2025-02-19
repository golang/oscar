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
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

const (
	syncProjectKind     = "gerrit.SyncProject"
	changeKind          = "gerrit.Change"
	commentKind         = "gerrit.Comment"
	changeUpdateKind    = "gerrit.ChangeUpdate"
	changeMergeableKind = "gerrit.ChangeMergeable"
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
//	["gerrit.SyncProject", Instance, Project] => JSON of projectSync structure
//	["gerrit.Change", Instance, Project, ChangeNumber] => ChangeInfo JSON
//	["gerrit.Comment", Instance, Project, ChangeNumber] => CommentInfo JSON
//	["gerrit.ChangeUpdate", Instance, ChangeNumber, MetaID] => DBTime
//	["gerrit.ChangeUpdateByTime", DBTime, Instance, ChangeNumber, MetaID] => []
//	["gerrit.ChangeMergeableTime", Instance, Project] => time
//	["gerrit.ChangeMergeable", Instance, Project, ChangeNumber] => bool
//
// A watcher on "gerrit.ChangeUpdate" will see all Gerrit changes,
// and can read the new data from the database.

// Gerrit APIs for searching changes return results only in reverse
// chronological order. As execution of [Client.Sync] can in principle
// be interrupted by the enclosing environment (for instance, Cloud Run
// timeout), this requires a different algorithm for making partial progress.
//
// The algorithm keeps track of three points in time: low watermark (L),
// high watermark (H), and current watermark (C). [Client.Sync] has
// processed all change updates before L and none after H. The algorithm
// first tries to process change updates in the interval [L, H] by going
// backwards from H. The watermark C is used to remember where in this
// interval the algorithm is currently. This is done so that the algorithm
// can restart in case there is an interruption. Once the algorithm
// processes the [L, H] interval, H becomes the new low watermark, the
// new high watermark is the current moment in time, and C is equal to H.

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

	ac accountCache

	testMu     sync.Mutex
	testClient *TestingClient
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
	}
}

var _ docs.Source[*ChangeEvent] = (*Client)(nil)

const DocWatcherID = "gerritrelateddocs"

// DocWatcher returns the change event watcher with name "gerritrelateddocs".
// Implements [docs.Source.DocWatcher].
func (c *Client) DocWatcher() *timed.Watcher[*ChangeEvent] {
	return c.ChangeWatcher(DocWatcherID)
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
	Instance    string // instance host name, "go-review.googlesource.com"
	Name        string // project name, such as "go" or "oscar".
	LowMark     string // low watermark L, in gerrit timestamp layout
	HighMark    string // high watermark H, in gerrit timestamp layout
	CurrentMark string // current watermark C, in gerrit timestamp layout
	// Skip is used to guarantee partial progress in the
	// case there are more change updates happening at
	// the CurrentMark across the change batch boundaries.
	Skip int
}

// store stores inst into db.
func (proj *projectSync) store(db storage.DB) {
	db.Set(o(syncProjectKind, proj.Instance, proj.Name), storage.JSON(proj))
}

// Add adds a Gerrit project such as "go" or "oscar" to the database.
// It only adds the project sync metadata.
// The initial data fetch does not happen until [Sync] or [SyncProject]
// is called.
// If the project is already present, Add does nothing and returns nil.
func (c *Client) Add(project string) error {
	key := o(syncProjectKind, c.instance, project)
	if _, ok := c.db.Get(key); ok {
		c.slog.Info("gerrit.Add: already present", "project", project)
		return nil
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
	for project := range c.projects() {
		if err := c.SyncProject(ctx, project); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// projects returns an iterator over all Gerrit projects in the client's
// database.
func (c *Client) projects() iter.Seq[string] {
	return func(yield func(string) bool) {
		for key := range c.db.Scan(o(syncProjectKind, c.instance), o(syncProjectKind, c.instance, ordered.Inf)) {
			var project string
			if err := ordered.Decode(key, nil, nil, &project); err != nil {
				c.db.Panic("gerrit client projects decode", "key", storage.Fmt(key), "err", err)
			}
			if !yield(project) {
				return
			}
		}
	}
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
		return fmt.Errorf("missing project %s", project)
	} else if err := json.Unmarshal(val, &proj); err != nil {
		return err
	}

	return c.syncChanges(ctx, &proj)
}

// syncChanges attempts to finish finding all the change updates
// in the interval [proj.LowMark, proj.HighMark]. If it successfully
// finishes analyzing the interval, it opens up a new one and
// starts working on it. It repeats this process as long as there
// are some changes processed in the interval.
// It stores the data for those changes in the database.
// It also adds in the metadata changes, such as values for watermarks.
func (c *Client) syncChanges(ctx context.Context, proj *projectSync) (err error) {
	// save stores the new values for low and high
	// watermark, and sets the current mark to high.
	save := func(low, high string) {
		proj.LowMark = low
		proj.HighMark = high
		proj.CurrentMark = high
		proj.Skip = 0
		proj.store(c.db)
		c.db.Flush()
	}

	// If the previous interval was closed successfully,
	// then create a new one.
	if proj.HighMark == "" {
		save(proj.LowMark, now())
	}

	for {
		c.slog.Info("gerrit sync interval", "project", proj.Name, "low", proj.LowMark,
			"curr", proj.CurrentMark, "skip", proj.Skip, "high", proj.HighMark)
		some, err := c.syncIntervalChanges(ctx, proj)
		if err != nil {
			return err
		}
		if !some { // no changes in the interval
			break
		}
		save(proj.HighMark, now()) // set high as the low mark
	}

	// Prepare for the next invocation of syncChanges.
	save(proj.HighMark, "") // set high as the low mark
	return nil
}

// testNow exists for testing purposes, to avoid the
// issue of dealing with the current moment in time.
// For ordinary use this should be empty string.
// TODO: instead, should we ask database for its
// definition of now?
var testNow string

// now returns current time in gerrit time format.
func now() string {
	if testNow != "" {
		return testNow
	}
	return time.Now().Format(timeStampLayout)
}

// syncIntervalChanges syncs changes in [proj.LowMark, proj.CurrentMark].
// Reports whether there were any change updates in the interval.
func (c *Client) syncIntervalChanges(ctx context.Context, proj *projectSync) (some bool, err error) {
	b := c.db.Batch()
	defer func() {
		b.Apply()
		c.db.Flush()
	}()

	// When we need to fetch multiple lists of changes,
	// concurrent modifications can cause us to see the
	// same change more than once. Keep track of the changes
	// we've already seen.
	cache := make(map[int]json.RawMessage)
	seen := func(change json.RawMessage, changeNum int, metaID string) (bool, error) {
		if oldChange, ok := cache[changeNum]; ok {
			same, err := sameChangeInfo(change, metaID, oldChange)
			if err != nil {
				return false, err
			}
			if same {
				// Nothing has changed.
				return true, nil
			}
		}
		cache[changeNum] = change

		key := o(changeKind, c.instance, proj.Name, changeNum)
		if oldChange, ok := c.db.Get(key); ok {
			same, err := sameChangeInfo(change, metaID, oldChange)
			if err != nil {
				return false, err
			}
			if same {
				// Nothing has changed.
				return true, nil
			}
		}
		return false, nil
	}

	saveCurrentMark := func(curr string, skip int) {
		proj.CurrentMark = curr
		proj.Skip = skip
		proj.store(c.db)
	}

	for {
		nChanges := 0
		for change, err := range c.changes(ctx, proj.Name, proj.LowMark, proj.CurrentMark, proj.Skip) {
			if err != nil {
				return false, err
			}
			if err := ctx.Err(); err != nil {
				return false, err
			}

			some = true
			nChanges++

			if c.flushRequested.Load() {
				// Flush database.
				b.Apply()
				c.db.Flush()
				c.flushRequested.Store(false)
			}

			// Change is a Gerrit ChangeInfo in JSON form.
			// Pull out the change number.
			var num struct {
				Number int    `json:"_number"`
				MetaID string `json:"meta_rev_id"`
			}
			if err := json.Unmarshal(change, &num); err != nil {
				return false, err
			}
			changeNum := num.Number
			metaID := num.MetaID
			if changeNum == 0 {
				return false, fmt.Errorf("missing _number field in %q", change)
			}
			if metaID == "" {
				return false, fmt.Errorf("missing meta_rev_id field in change %d: %q", changeNum, change)
			}

			same, err := seen(change, changeNum, metaID)
			if err != nil {
				return false, err
			}
			if !same {
				key := o(changeKind, c.instance, proj.Name, changeNum)
				b.Set(key, change)
				if err := c.syncComments(ctx, b, proj.Name, changeNum); err != nil {
					return false, err
				}

				// Record that the change was updated.
				timed.Set(c.db, b, changeUpdateKind, o(c.instance, changeNum, metaID), nil)
			}

			b.MaybeApply()

			// Save the update time of the most recent
			// change to proj.CurrentMark only once we
			// have successfully processed the change.
			var updated struct {
				Updated string `json:"updated"`
			}
			if err := json.Unmarshal(change, &updated); err != nil {
				return false, err
			}
			// Gerrit intervals are inclusive. Update proj.Skip
			// to avoid re-fetching processeed change updates
			// happening at the same gerrit timestamp at the
			// boundaries of the outter loop.
			if updated.Updated == proj.CurrentMark {
				saveCurrentMark(updated.Updated, proj.Skip+1)
			} else {
				saveCurrentMark(updated.Updated, 1)
			}

			// Flush progress to the database occasionally
			// to make sure it is saved before interruption.
			if nChanges%100 == 0 {
				b.Apply()
				c.db.Flush()
			}
		}

		// There were no changes in the interval [proj.LowMark, proj.CurrentMark],
		// which means we are done with the interval.
		if nChanges == 0 {
			return some, nil
		}
	}

	return some, nil
}

// syncComments updates the comments of a change in the database.
func (c *Client) syncComments(ctx context.Context, b storage.Batch, project string, changeNum int) error {
	var obj json.RawMessage
	if c.divertChanges() { // testing
		c.testMu.Lock()
		cms := c.testClient.comments[changeNum]
		c.testMu.Unlock()
		obj = storage.JSON(map[string][]*CommentInfo{"file": cms}) // attach comments to a single file
	} else {
		url := "https://" + c.instance + "/changes/" + strconv.Itoa(changeNum) + "/comments"
		if err := c.get(ctx, url, &obj); err != nil {
			return err
		}
	}

	key := o(commentKind, c.instance, project, changeNum)
	b.Set(key, obj)
	return nil
}

const gerritQueryLimit = 500 // gerrit returns up to 500 changes at a time.

// changes returns an iterator, in reverse chronological order, over
// at most gerritQueryLimit changes in the Gerrit repo most recently
// updated in the interval [before, after]. The first skip number of
// changes matching the criteria are disregarded.
// Empty strings for before and after indicate open interval.
func (c *Client) changes(ctx context.Context, project, after, before string, skip int) iter.Seq2[json.RawMessage, error] {
	if c.divertChanges() { // testing
		return c.testClient.changes(ctx, project, after, before, skip)
	}

	return func(yield func(json.RawMessage, error) bool) {
		baseURL := "https://" + c.instance + "/changes"

		values := url.Values{
			"o": changeOpts,
		}
		query := "p:" + project
		if after != "" {
			query += " after:" + quote(after) // precise timestamps have spaces and need quotes
		}
		if before != "" {
			query += " before:" + quote(before) // precise timestamps have spaces and need quotes
		}
		query += " limit:" + strconv.Itoa(gerritQueryLimit)
		values.Set("q", query)
		if skip > 0 {
			values.Set("S", strconv.Itoa(skip))
		}
		addr := baseURL + "?" + values.Encode()

		var body []json.RawMessage
		if err := c.get(ctx, addr, &body); err != nil {
			yield(nil, err)
			return
		}

		for _, change := range body {
			if !yield(change, nil) {
				return
			}
		}
	}
}

func quote(t string) string {
	if _, err := strconv.Unquote(t); err != nil { // missing quotes
		return strconv.Quote(t)
	}
	return t
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

				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(backoff):
				}

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

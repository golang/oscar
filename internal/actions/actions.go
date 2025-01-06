// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package actions implements a log of actions in the database.
An action is anything that affects the outside world, such as
edits to GitHub or Gerrit.

The action log uses database keys beginning with "action.Log" and an "action kind"
string that describes the rest of the key and the format of the action and its result.
The caller provides the rest of the key.
For example, GitHub issue keys look like

	["action.Log", "githubIssue", project, issue]

Action log values are described by the [Entry] type. Values
include the parts of the key, an encoded action that provides
enough information to perform it, and the result of the action
after it is carried out. There are also fields for approval,
discussed below.

Components that wish to use the action log must call [Register] to install a unique
action kind along with a function that will run the action. Register returns a "before
function" to call to add the action to the log before it is run. By choosing a key
for the action that corresponds to an activity that it wishes to perform only once,
a component can avoid duplicate executions of an action. For example, if a component
wants to edit a GitHub comment only once, the key can be the URL for that comment.
The before function will not write an action to the log if its key is already present.
It returns false in this case, but the component is free to ignore this value.

Once it has called the before function (typically in its Run method), the component
has nothing more to do at that time. At some later time, this package's [Run] function
will be called to execute all pending actions (those added to the log but not yet
run). Run will use the action kind of the pending action to dispatch to the registered
run function, returning control to the component that logged the action.

# Approvals

Some actions require approval before they can be executed.
[Entry.ApprovalRequired] represents that, and [Entry.Decisions]
records whether the action was approved or denied, by whom, and when.
An action may be approved or denied multiple times.
Approval is denied if there is at least one denial.

# Other DB entries

This package stores other relationships in the database besides
the log entries.

Keys beginning with "action.Pending" store the list of pending actions. The rest
of the key is the action's key, and the value is nil. Actions are added to the pending
list when they are first logged, and removed when they have been approved (if needed)
and executed. (We cannot use a [timed.Watcher] for this purpose, because approvals can
happen out of order.)

Keys beginning with "action.Wallclock" map wall clock times ([time.Time] values)
to DBTimes. The mapping facilitates common log queries, like "show me the last hour
of logs." The keys have the form

	["action.Wallclock", time.Time.UnixNanos, DBTime]

The values are nil. Storing both times in the key permits multiple DBTimes for the
same wall clock time.
*/
package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

const (
	logKind     = "action.Log"       // everything in the log
	wallKind    = "action.Wallclock" // mapping from time.Time to timed.DBTime
	pendingKind = "action.Pending"   // unexecuted actions
)

// An Entry is one entry in the action log.
type Entry struct {
	Created time.Time    // time of the Before call
	Kind    string       // determines the format of Key, Action and Result
	Key     []byte       // user-provided part of the key; arg to Before and After
	ModTime timed.DBTime // set by Get and ScanAfter, used to resume scan
	Action  []byte       // encoded action
	// Fields set by After
	Done   time.Time // time of the After call, or 0 if not called
	Result []byte    // encoded result
	Error  string    // error from attempted action, "" on success
	// Fields for approval
	ApprovalRequired bool
	Decisions        []Decision // approval decisions
}

// IsDone reports whether e is done.
func (e *Entry) IsDone() bool {
	return !e.Done.IsZero()
}

func (e *Entry) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "<Entry Created:%s", e.Created.Format(time.RFC3339))
	fmt.Fprintf(&b, " Kind:%s", e.Kind)
	fmt.Fprintf(&b, " Key:%s", storage.Fmt(e.Key))
	fmt.Fprintf(&b, " Action:%q", e.Action)
	fmt.Fprintf(&b, " Done:%s", e.Done.Format(time.RFC3339))
	fmt.Fprintf(&b, ">")
	return b.String()
}

// ActionForDisplay looks up the action associated with e and calls [Actioner.StringForDisplay]
// on it.
func (e *Entry) ActionForDisplay() string {
	a := lookupActioner(e.Kind)
	if a == nil {
		return string(e.Action)
	}
	return a.ForDisplay(e.Action)
}

// A Decision describes the approval or denial of an action.
type Decision struct {
	Name     string    // name of person or system making the decision
	Time     time.Time // time of the decision
	Approved bool      // true if approved, false if denied
}

// RequiresApproval can be passed as the last argument to a [BeforeFunc] for clarity.
const RequiresApproval = true

// entry is the database representation of Entry.
// Changes to this struct must still allow existing values from the database to be
// unmarshaled. Fields can be added or removed, but their names must not change,
// and their types can only change slightly (uint to uint64 for example, but not uint
// to bool).
//
// By using a DB representation that is not part of the API, we can modify the API
// more freely without needing to reformat every entry in the DB. For example,
// we can decide that a single Decision is enough and change [Entry] accordingly,
// while the DB entries still have lists of decisions (which we would have to collapse
// somehow into a single one to create an Entry).
type entry struct {
	Created          time.Time
	Kind             string
	Key              []byte
	ModTime          timed.DBTime
	Action           []byte
	Done             time.Time
	Result           []byte
	Error            string
	ApprovalRequired bool
	Decisions        []decision
}

// decision is the database representation of Decision.
// Changes to this struct must still allow existing values from the database to be
// unmarshaled.
type decision struct {
	Name     string
	Time     time.Time
	Approved bool
}

func toEntry(e *entry) *Entry {
	e2 := &Entry{
		Created:          e.Created,
		Kind:             e.Kind,
		Key:              e.Key,
		ModTime:          e.ModTime,
		Action:           e.Action,
		Done:             e.Done,
		Result:           e.Result,
		Error:            e.Error,
		ApprovalRequired: e.ApprovalRequired,
	}
	for _, d := range e.Decisions {
		e2.Decisions = append(e2.Decisions, Decision(d))
	}
	return e2
}

func fromEntry(e *Entry) *entry {
	e2 := &entry{
		Created:          e.Created,
		Kind:             e.Kind,
		Key:              e.Key,
		ModTime:          e.ModTime,
		Action:           e.Action,
		Done:             e.Done,
		Result:           e.Result,
		Error:            e.Error,
		ApprovalRequired: e.ApprovalRequired,
	}
	for _, d := range e.Decisions {
		e2.Decisions = append(e2.Decisions, decision(d))
	}
	return e2
}

// before adds an action to the db if it is not already present.
// For more, see [BeforeFunc].
func before(db storage.DB, actionKind string, key, action []byte, requiresApproval bool) bool {
	unlock := lockAction(db, actionKind, key)
	defer unlock()

	dkey := dbKey(actionKind, key)
	if _, ok := timed.Get(db, logKind, dkey); ok {
		return false
	}
	e := &entry{
		Created:          time.Now(), // wall clock time
		Kind:             actionKind,
		Key:              key,
		Action:           action,
		ApprovalRequired: requiresApproval,
	}
	setEntry(db, dkey, e)
	return true
}

// Get looks up the Entry associated with the given arguments.
// If there is no entry for key in the database, Get returns nil, false.
// Otherwise it returns the entry and true.
func Get(db storage.DB, actionKind string, key []byte) (*Entry, bool) {
	dkey := dbKey(actionKind, key)
	e, ok := getEntry(db, dkey)
	if !ok {
		return nil, false
	}
	return toEntry(e), true
}

// ReRunAction attempts to re-run a single action denoted by the given kind and key.
// Only actions that have previously failed can be re-run.
func ReRunAction(ctx context.Context, lg *slog.Logger, db storage.DB, actionKind string, key []byte) (err error) {
	dkey := dbKey(actionKind, key)
	defer func() {
		if err != nil {
			err = fmt.Errorf("actions.ReRun(%s): %w", storage.Fmt(dkey), err)
		}
	}()

	lockName := logKind + "-" + string(dkey)
	db.Lock(lockName)
	defer db.Unlock(lockName)

	e, ok := getEntry(db, dkey)
	if !ok {
		return errors.New("not found")
	}
	if !e.approved() {
		return errors.New("not approved")
	}
	if e.Done.IsZero() {
		return errors.New("not done")
	}
	if e.Error == "" {
		return errors.New("did not fail")
	}
	return runEntry(ctx, lg, db, e)
}

// AddDecision adds a Decision to the action referred to by actionKind,
// key and u.
// It panics if the action does not exist or does not require approval.
func AddDecision(db storage.DB, actionKind string, key []byte, d Decision) {
	unlock := lockAction(db, actionKind, key)
	defer unlock()

	dkey := dbKey(actionKind, key)
	te, ok := timed.Get(db, logKind, dkey)
	if !ok {
		// unreachable unless program bug
		db.Panic("actions.AddDecision: does not exist", "dkey", dkey)
	}
	e := unmarshalTimedEntry(te)
	if !e.ApprovalRequired {
		// unreachable unless program bug
		db.Panic("actions.AddDecision: approval not required", "dkey", dkey)
	}
	e.Decisions = append(e.Decisions, decision(d))
	setEntry(db, dkey, e)
}

// Approved reports whether the Entry represents an action that can be
// be executed. It returns true for actions that do not require approval
// and for those that do with at least one Decision and no denials. (In other
// words, a single denial vetoes the action.)
func (e *Entry) Approved() bool {
	return fromEntry(e).approved()
}

func (e *entry) approved() bool {
	if !e.ApprovalRequired {
		return true
	}
	if len(e.Decisions) == 0 {
		return false
	}
	for _, d := range e.Decisions {
		if !d.Approved {
			return false
		}
	}
	return true
}

// Scan returns an iterator over action log entries with start ≤ key ≤ end.
// Keys begin with the actionKind string, followed by the key provided to [Before],
// followed by the uint64 returned by Before.
func Scan(db storage.DB, start, end []byte) iter.Seq[*Entry] {
	return func(yield func(*Entry) bool) {
		for te := range timed.Scan(db, logKind, start, end) {
			if !yield(toEntry(unmarshalTimedEntry(te))) {
				break
			}
		}
	}
}

// ScanAfterDBTime returns an iterator over action log entries
// that were started after DBTime t.
// If filter is non-nil, ScanAfterDBTime omits entries for which filter(actionKind, key) returns false.
func ScanAfterDBTime(lg *slog.Logger, db storage.DB, t timed.DBTime, filter func(actionKind string, key []byte) bool) iter.Seq[*Entry] {
	tfilter := func(key []byte) bool {
		if filter == nil {
			return true
		}
		var ns string
		rest, err := ordered.DecodePrefix(key, &ns)
		if err != nil {
			// unreachable unless db corruption
			db.Panic("actions.ScanAfter: decode", "key", storage.Fmt(key))
		}
		return filter(ns, rest)
	}

	return func(yield func(*Entry) bool) {
		for te := range timed.ScanAfter(lg, db, logKind, t, tfilter) {
			if !yield(toEntry(unmarshalTimedEntry(te))) {
				break
			}
		}
	}
}

// ScanAfter returns an iterator over action log entries that were started after time t.
// If filter is non-nil, ScanAfter omits entries for which filter(actionKind, key) returns false.
func ScanAfter(lg *slog.Logger, db storage.DB, t time.Time, filter func(actionKind string, key []byte) bool) iter.Seq[*Entry] {
	// Find the first DBTime associated with a time after t.
	// If there is none, use the maximum DBTime.
	dbt := int64(math.MaxInt64)
	for key := range db.Scan(ordered.Encode(wallKind, t.UnixNano()+1), ordered.Encode(wallKind, ordered.Inf)) {
		// The DBTime is the third part of the key, after wallKind and the time.Time.
		if err := ordered.Decode(key, nil, nil, &dbt); err != nil {
			// unreachable unless corrupt DB
			db.Panic("ScanAfter decode", "key", key, "err", err)
		}
		break
	}
	// dbt is the DBTime corresponding to t+1. Adjust to approximate
	// the DBTime for t.
	dbt--
	return ScanAfterDBTime(lg, db, timed.DBTime(dbt), filter)
}

var registry sync.Map

func lookupActioner(actionKind string) Actioner {
	a, ok := registry.Load(actionKind)
	if !ok {
		return nil
	}
	return a.(Actioner)
}

// An Actioner works with actions.
// Actioners are registered with [Register]
type Actioner interface {
	// Run deserializes the action, executes it, then return
	// the serialized result and error.
	Run(context.Context, []byte) ([]byte, error)
	// ForDisplay returns a string describing the action in a way that is suitable
	// for display on web pages and by command-line tools.
	// The action is provided in serialized form, as with Run.
	ForDisplay([]byte) string
}

// BeforeFunc is the type of functions that are called to log an action before it is run.
// It writes an entry to db's action log with the given key and a representation
// of the action. The key must be created with [ordered.Encode].
// The action should be JSON-encoded so tools can process it.
//
// The function reports whether the action was added to the DB, or is a duplicate
// (has the same key) of an action that is already in the log.
type BeforeFunc func(db storage.DB, key, action []byte, requiresApproval bool) (added bool)

// Register associates the given action kind and [Actioner].
// Only Actioner may be registered for each kind, except during testing,
// when Register always registers its arguments.
//
// Register returns a function that should be called to log an action before it is run.
func Register(actionKind string, a Actioner) BeforeFunc {
	if testing.Testing() {
		registry.Store(actionKind, a)
	} else if _, ok := registry.LoadOrStore(actionKind, a); ok {
		panic(fmt.Sprintf("%q already registered", actionKind))
	}
	return func(db storage.DB, key, action []byte, requiresApproval bool) bool {
		return before(db, actionKind, key, action, requiresApproval)
	}
}

// Run runs all actions that are ready to run, in the order they were added.
// An action is ready to run if it is approved and has not already run.
// Run returns the errors of all failed actions.
func Run(ctx context.Context, lg *slog.Logger, db storage.DB) error {
	// Scan all pending actions, from earliest to latest.
	var errs []error
	for te := range timed.ScanAfter(lg, db, pendingKind, 0, nil) {
		if err := maybeRunEntry(ctx, lg, db, te.Key); err != nil {
			lg.Error("action failed", "key", storage.Fmt(te.Key), "err", err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// maybeRunEntry runs the entry with dkey if it is ready.
// It locks the entry's DB key so that it can check the entry's status and run it atomically.
func maybeRunEntry(ctx context.Context, lg *slog.Logger, db storage.DB, dkey []byte) error {
	// dkey includes the action kind and user key (third arg to [before]), but not the logKind.
	// e.Key is only the user key.
	lockName := logKind + "-" + string(dkey)
	db.Lock(lockName)
	defer db.Unlock(lockName)

	e, ok := getEntry(db, dkey)
	if !ok {
		// unreachable unless bug in this package
		db.Panic("pending action not found", "key", storage.Fmt(dkey))
	}
	if !e.Done.IsZero() {
		// This action was already run. It should have been removed from the pending list.
		return fmt.Errorf("done action %s on pending list", storage.Fmt(dkey))
	}
	if !e.approved() {
		return nil
	}
	return runEntry(ctx, lg, db, e)
}

// runEntry runs the action in entry e. It assumes it is ready to run (and so must
// be called with a lock held). It returns the error resulting from the run.
func runEntry(ctx context.Context, lg *slog.Logger, db storage.DB, e *entry) error {
	a := lookupActioner(e.Kind)
	if a == nil {
		// unreachable unless bug, or if an action kind was removed
		// while there were still unfinished actions
		db.Panic("unregistered action kind", "kind", e.Kind)
	}
	lg.Info("action log: running", "kind", e.Kind, "key", storage.Fmt(e.Key))
	result, err := a.Run(ctx, e.Action)
	// mark done
	e.Done = time.Now()
	e.Result = result
	if err != nil {
		e.Error = err.Error()
	}
	setEntry(db, dbKey(e.Kind, e.Key), e)
	return err
}

// ClearLogForTesting deletes the entire action log.
// It is intended only for tests.
func ClearLogForTesting(_ *testing.T, db storage.DB) {
	// Additional ugly sanity check.
	if dbt := fmt.Sprintf("%T", db); dbt != "*storage.memDB" {
		db.Panic("ClearLogForTesting: bad type", "type", dbt)
	}
	db.DeleteRange(ordered.Encode(logKind), ordered.Encode(logKind, ordered.Inf))
}

// unmarshalTimedEntry extracts an entry from a timed.Entry.
func unmarshalTimedEntry(te *timed.Entry) *entry {
	var e entry
	if err := json.Unmarshal(te.Val, &e); err != nil {
		// unreachable unless bug in this package
		storage.Panic("actions.After: json.Unmarshal entry", "dkey", storage.Fmt(te.Key), "err", err)
	}
	e.ModTime = te.ModTime
	return &e
}

// lockAction locks the action with the given kind and key in db, and returns a function
// that unlocks it.
func lockAction(db storage.DB, actionKind string, key []byte) func() {
	// This name must match the name used in maybeRunEntry and other functions
	// that may not have the action kind.
	name := logKind + "-" + string(dbKey(actionKind, key))
	db.Lock(name)
	return func() { db.Unlock(name) }
}

func getEntry(db storage.DB, dkey []byte) (*entry, bool) {
	te, ok := timed.Get(db, logKind, dkey)
	if !ok {
		return nil, false
	}
	return unmarshalTimedEntry(te), true
}

func setEntry(db storage.DB, dkey []byte, e *entry) {
	if e.Created.IsZero() {
		// unreachable unless there is a bug in this package
		db.Panic("zero Created", "dkey", storage.Fmt(dkey))
	}
	b := db.Batch()
	dtime := timed.Set(db, b, logKind, dkey, storage.JSON(e))
	var t time.Time
	if e.Done.IsZero() {
		// This action hasn't run; add it to the list of pending actions.
		timed.Set(db, b, pendingKind, dkey, nil)
		t = e.Created
	} else {
		// This action has run; delete it from the list of pending actions.
		timed.Delete(db, b, pendingKind, dkey)
		t = e.Done
	}
	// Associate the dtime with the entry's done or created times.
	b.Set(ordered.Encode(wallKind, t.UnixNano(), int64(dtime)), nil)
	b.Apply()
}

func dbKey(actionKind string, userKey []byte) []byte {
	k := ordered.Encode(actionKind)
	return append(k, userKey...)
}

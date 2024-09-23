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

Call [Before] before performing an action. It will return
a function to call after the action completes.

# Approvals

Some actions require approval before they can be executed.
[Entry.ApprovalRequired] represents that, and [Entry.Decisions]
records whether the action was approved or denied, by whom, and when.
An action may be approved or denied multiple times.
Approval is denied if there is at least one denial.

# Other DB entries

This package stores other relationships in the database besides
the log entries.

Keys beginning with "action.Wallclock" map wall clock times ([time.Time] values)
to DBTimes. The mapping facilitates common log queries, like "show me the last hour
of logs." The keys have the form

	["action.Wallclock", time.Time.UnixNanos, DBTime]

The values are nil. Storing both times in the key permits multiple DBTimes for the
same wall clock time.
*/
package actions

import (
	"encoding/json"
	"iter"
	"log/slog"
	"math"
	"time"

	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

const (
	logKind  = "action.Log"
	wallKind = "action.Wallclock" // mapping from time.Time to timed.DBTime
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

// A Decision describes the approval or denial of an action.
type Decision struct {
	Name     string    // name of person or system making the decision
	Time     time.Time // time of the decision
	Approved bool      // true if approved, false if denied
}

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

// Before writes an entry to db's action log with the given action kind,
// a representation of the action, and an additional key for the entry.
// The key must be created with [ordered.Encode].
// The action can be encoded however the user wishes, but if a string, ordered.Encode
// or JSON is used, then [storage.Fmt] can print the action readably.
//
// If requiresApproval is true, then Approve must be called before the action
// can be executed.
//
// Before returns a []byte that is the full database key, incorporating the action
// kind and user key.
// It should be passed to [After] after the action completes.
// Example:
//
//	const actionKind = "githubIssues"
//	key := ordered.Encode{"golang/go", 123}
//	dbkey := actions.Before(db, actionKind, key, addCommentAction, false)
//	res, err := addTheComment()
//	actions.After(dbkey, res, err)
//	if err != nil {...}
func Before(db storage.DB, actionKind string, key, action []byte, requiresApproval bool) []byte {
	dkey := dbKey(actionKind, key)
	e := &entry{
		Created:          time.Now(), // wall clock time
		Kind:             actionKind,
		Key:              key,
		Action:           action,
		ApprovalRequired: requiresApproval,
	}
	setEntry(db, dkey, e)
	return dkey
}

// After records an action's completion in the action log.
// The dbkey argument must come from a call to [Before], or
// from [Entry.DBKey].
// The result argument is the result of the action if it succeeded.
// The err argument is the error returned from attempting the action,
// or nil for success.
// After panics if the action does not exist, or if After has already been
// called on it.
func After(db storage.DB, dbkey, result []byte, err error) {
	// Guard against concurrent calls on the same entry.
	lock := string(dbkey)
	db.Lock(lock)
	defer db.Unlock(lock)

	te, ok := timed.Get(db, logKind, dbkey)
	if !ok {
		db.Panic("actions.After: missing action", "dkey", storage.Fmt(dbkey))
	}
	e := unmarshalTimedEntry(te)
	if !e.Done.IsZero() {
		db.Panic("actions.After: already called", "dkey", storage.Fmt(dbkey))
	}
	e.Done = time.Now()
	e.Result = result
	if err != nil {
		e.Error = err.Error()
	}
	setEntry(db, dbkey, e)
}

// Get looks up the Entry associated with the given arguments.
// If there is no entry for key in the database, Get returns nil, false.
// Otherwise it returns the entry and true.
func Get(db storage.DB, actionKind string, key []byte) (*Entry, bool) {
	dkey := dbKey(actionKind, key)
	return getEntry(db, dkey)
}

func getEntry(db storage.DB, dkey []byte) (*Entry, bool) {
	te, ok := timed.Get(db, logKind, dkey)
	if !ok {
		return nil, false
	}
	e := unmarshalTimedEntry(te)
	return toEntry(e), true
}

// AddDecision adds a Decision to the action referred to by actionKind,
// key and u.
// It panics if the action does not exist or does not require approval.
func AddDecision(db storage.DB, actionKind string, key []byte, d Decision) {
	dkey := dbKey(actionKind, key)
	lockName := logKind + "-" + string(dkey)
	db.Lock(lockName)
	defer db.Unlock(lockName)

	te, ok := timed.Get(db, logKind, dkey)
	if !ok {
		db.Panic("actions.AddDecision: does not exist", "dkey", dkey)
	}
	e := unmarshalTimedEntry(te)
	if !e.ApprovalRequired {
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

func (e *Entry) DBKey() []byte {
	return dbKey(e.Kind, e.Key)
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
	dbt := math.MaxInt64
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

func unmarshalTimedEntry(te *timed.Entry) *entry {
	var e entry
	if err := json.Unmarshal(te.Val, &e); err != nil {
		storage.Panic("actions.After: json.Unmarshal entry", "dkey", storage.Fmt(te.Key), "err", err)
	}
	e.ModTime = te.ModTime
	return &e
}

func setEntry(db storage.DB, dkey []byte, e *entry) {
	b := db.Batch()
	dtime := timed.Set(db, b, logKind, dkey, storage.JSON(e))
	// Associate the dtime with the entry's done or created times.
	if e.Created.IsZero() {
		db.Panic("zero Created", "dkey", storage.Fmt(dkey))
	}
	t := e.Created
	if !e.Done.IsZero() {
		t = e.Done
	}
	b.Set(ordered.Encode(wallKind, t.UnixNano(), int64(dtime)), nil)
	b.Apply()
}

func dbKey(actionKind string, userKey []byte) []byte {
	k := ordered.Encode(actionKind)
	return append(k, userKey...)
}

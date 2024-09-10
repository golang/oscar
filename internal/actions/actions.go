// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package actions implements a log of actions in the database.
An action is anything that affects the outside world, such as
edits to GitHub or Gerrit.

The action log uses database keys beginning with "actions.Log"
and a namespace that describes the rest of the key and the
format of the action and its result.
All entry keys end with a random ID to ensure they are unique.
The caller provides the part of the key between the namespace and the random value.
For example, GitHub issue keys look like

	["actions.Log", "githubIssue", project, issue, random]

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
*/
package actions

import (
	"encoding/json"
	"iter"
	"math/rand/v2"
	"time"

	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"rsc.io/ordered"
)

const (
	logKind = "action.Log"
)

// An Entry is one entry in the action log.
type Entry struct {
	Created   time.Time    // time of the Before call
	Namespace string       // what the action applies to: GitHub issue, etc.
	Key       []byte       // user-provided part of the key; arg to Before and After
	Unique    uint64       // last component of the actual key
	ModTime   timed.DBTime // set by Get and ScanAfter, used to resume scan
	Action    []byte       // encoded action
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
	Namespace        string
	Key              []byte
	Unique           uint64
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
		Namespace:        e.Namespace,
		Key:              e.Key,
		Unique:           e.Unique,
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
		Namespace:        e.Namespace,
		Key:              e.Key,
		Unique:           e.Unique,
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

// Before writes an entry to db's action log with the given namespace,
// a representation of the action, and an additional key for the entry.
// The key must be created with [ordered.Encode].
// The action can be encoded however the user wishes, but if a string, ordered.Encode
// or JSON is used, then [storage.Fmt] can print the action readably.
//
// If requiresApproval is true, then Approve must be called before the action
// can be executed.
// It returns a function that should be called after the action completes with the result
// and error.
// Example:
//
//	var res []byte
//	var err error
//	after := Before(db, "githubIssues", ordered.Encode{"golang/go", 123}, addCommentAction, false)
//	defer func() { after(res, err) }()
func Before(db storage.DB, namespace string, key, action []byte, requiresApproval bool) func(result []byte, err error) {
	dkey := before(db, namespace, key, action, requiresApproval)
	return func(result []byte, err error) {
		after(db, dkey, result, err)
	}
}

func before(db storage.DB, namespace string, key, action []byte, requiresApproval bool) []byte {
	u := rand.Uint64()
	dkey := dbKey(namespace, key, u)
	e := &entry{
		Created:          time.Now(), // wall clock time
		Namespace:        namespace,
		Key:              key,
		Unique:           u,
		Action:           action,
		ApprovalRequired: requiresApproval,
	}
	setEntry(db, dkey, e)
	return dkey
}

// After records an action's completion in the action log.
// The namespace and key arguments must match those passed to Before for
// this action, and u must be the return value of Before.
// The result argument is the result of the action if it succeeded.
// The err argument is the error returned from attempting the action,
// or nil for success.
// After panics if the action does not exist, or if After has already been
// called on it.
func after(db storage.DB, dkey, result []byte, err error) {
	// Guard against concurrent calls on the same entry.
	lock := string(dkey)
	db.Lock(lock)
	defer db.Unlock(lock)

	te, ok := timed.Get(db, logKind, dkey)
	if !ok {
		db.Panic("actions.After: missing action", "dkey", storage.Fmt(dkey))
	}
	e := unmarshalTimedEntry(te)
	if !e.Done.IsZero() {
		db.Panic("actions.After: already called", "dkey", storage.Fmt(dkey))
	}
	e.Done = time.Now()
	e.Result = result
	if err != nil {
		e.Error = err.Error()
	}
	setEntry(db, dkey, e)
}

// Get looks up the Entry associated with the given arguments.
// If there is no entry for key in the database, Get returns nil, false.
// Otherwise it returns the entry and true.
func Get(db storage.DB, namespace string, key []byte, unique uint64) (*Entry, bool) {
	dkey := dbKey(namespace, key, unique)
	return get(db, dkey)
}

func get(db storage.DB, dkey []byte) (*Entry, bool) {
	te, ok := timed.Get(db, logKind, dkey)
	if !ok {
		return nil, false
	}
	e := unmarshalTimedEntry(te)
	return toEntry(e), true
}

// AddDecision adds a Decision to the action referred to by namespace,
// key and u.
// It panics if the action does not exist or does not require approval.
func AddDecision(db storage.DB, namespace string, key []byte, u uint64, d Decision) {
	dkey := dbKey(namespace, key, u)
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

// Scan returns an iterator over action log entries with start ≤ key ≤ end.
// Keys begin with the namespace string, followed by the key provided to [Before],
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

// ScanAfter returns an iterator over action log entries
// that were started after DBTime t.
// If filter is non-nil, ScanAfter omits entries for which filter(namespace, key) returns false.
func ScanAfter(db storage.DB, t timed.DBTime, filter func(namespace string, key []byte) bool) iter.Seq[*Entry] {
	var tfilter func(key []byte) bool
	if filter != nil {
		tfilter = func(key []byte) bool {
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
	}

	return func(yield func(*Entry) bool) {
		for te := range timed.ScanAfter(db, logKind, t, tfilter) {
			if !yield(toEntry(unmarshalTimedEntry(te))) {
				break
			}
		}
	}
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
	timed.Set(db, b, logKind, dkey, storage.JSON(e))
	b.Apply()
}

func dbKey(namespace string, userKey []byte, u uint64) []byte {
	k := ordered.Encode(namespace)
	k = append(k, userKey...)
	return append(k, ordered.Encode(u)...)
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package actions

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	gcmp "github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
	"golang.org/x/oscar/internal/testutil"
	"rsc.io/ordered"
)

func TestDB(t *testing.T) {
	var (
		actionKind = "test"
		key        = ordered.Encode("num", 23)
		action     = []byte("action")
		result     = []byte("result")
		anError    = errors.New("bad")
	)
	t.Run("before", func(t *testing.T) {
		db := storage.MemDB()
		if !before(db, actionKind, key, action, !RequiresApproval) {
			t.Fatal("already added")
		}
		e, ok := Get(db, actionKind, key)
		if !ok {
			t.Fatal("not found")
		}
		want := &Entry{
			Created:          e.Created,
			Kind:             actionKind,
			Key:              key,
			Action:           action,
			ApprovalRequired: false,
			ModTime:          e.ModTime,
		}
		if !reflect.DeepEqual(e, want) {
			t.Errorf("Before:\ngot  %+v\nwant %+v", e, want)
		}

		if before(db, actionKind, key, action, !RequiresApproval) {
			t.Error("got added for existing action")
		}
	})
	t.Run("get not found", func(t *testing.T) {
		db := storage.MemDB()
		if _, ok := Get(db, actionKind, key); ok {
			t.Fatal("action present, should be missing")
		}
	})
	t.Run("approval", func(t *testing.T) {
		db := storage.MemDB()
		if !before(db, actionKind, key, action, RequiresApproval) {
			t.Fatal("already added")
		}
		tm := time.Now().Round(0).In(time.UTC)
		d1 := Decision{Name: "name1", Time: tm, Approved: true}
		d2 := Decision{Name: "name2", Time: tm, Approved: false}
		AddDecision(db, actionKind, key, d1)
		AddDecision(db, actionKind, key, d2)
		e, ok := Get(db, actionKind, key)

		if !ok {
			t.Fatal("not found")
		}
		want := &Entry{
			Created:          e.Created,
			ModTime:          e.ModTime,
			Kind:             actionKind,
			Key:              key,
			Action:           action,
			ApprovalRequired: true,
			Decisions:        []Decision{d1, d2},
		}
		if !reflect.DeepEqual(e, want) {
			t.Errorf("\ngot:  %+v\nwant: %+v", e, want)
		}
	})
	t.Run("scan", func(t *testing.T) {
		eqEntry := func(e1, e2 *Entry) bool {
			return reflect.DeepEqual(e1, e2)
		}

		db := storage.MemDB()
		lg := testutil.Slogger(t)
		var entries []*Entry
		start := time.Now()
		for i := 1; i <= 3; i++ {
			e := &Entry{
				Kind:   fmt.Sprintf("test-%d", i%2),
				Key:    ordered.Encode(i),
				Action: []byte{byte(-i)},
			}
			time.Sleep(50 * time.Millisecond) // ensure each action has a different wall clock time
			if !before(db, e.Kind, e.Key, e.Action, !RequiresApproval) {
				t.Fatal("already added")
			}
			entries = append(entries, e)
		}

		entriesByKey := slices.Clone(entries)
		slices.SortFunc(entriesByKey, func(e1, e2 *Entry) int {
			return cmp.Or(
				cmp.Compare(e1.Kind, e2.Kind),
				bytes.Compare(e1.Key, e2.Key),
			)
		})
		got := slices.Collect(Scan(db, nil, ordered.Encode(ordered.Inf)))
		for i, g := range got {
			if i < len(entriesByKey) {
				entriesByKey[i].Created = g.Created
				entriesByKey[i].ModTime = g.ModTime
			}
		}
		compareSlices(t, got, entriesByKey, eqEntry)

		got = slices.Collect(ScanAfterDBTime(lg, db, 0, nil))
		compareSlices(t, got, entries, func(e1, e2 *Entry) bool {
			return reflect.DeepEqual(e1, e2)
		})

		// Test filter.
		got = slices.Collect(ScanAfterDBTime(lg, db, 0, func(string, []byte) bool { return false }))
		if len(got) > 0 {
			t.Error("got entries, want none")
		}

		// Test that early break doesn't panic.
		for range Scan(db, nil, ordered.Encode(ordered.Inf)) {
			break
		}
		for range ScanAfterDBTime(lg, db, 0, nil) {
			break
		}

		for _, test := range []struct {
			t    time.Time
			want []*Entry
		}{
			{start, entries},
			{time.Now(), nil},
			{entries[0].Created, entries[1:]},
		} {
			got := slices.Collect(ScanAfter(lg, db, test.t, nil))
			compareSlices(t, got, test.want, eqEntry)
		}
	})
	t.Run("registerAndRun", func(t *testing.T) {
		var gotAction []byte
		before := Register(actionKind, testActioner{
			run: func(_ context.Context, action []byte) ([]byte, error) {
				gotAction = action
				return result, anError
			},
		})

		db := storage.MemDB()
		if !before(db, key, action, !RequiresApproval) {
			t.Fatal("already added")
		}
		e, ok := getEntry(db, dbKey(actionKind, key))
		if !ok {
			t.Fatal("missing entry")
		}
		runEntry(context.Background(), testutil.Slogger(t), db, e)
		if !bytes.Equal(gotAction, action) {
			t.Fatalf("got %q, want %q", gotAction, action)
		}
		e, ok = getEntry(db, dbKey(actionKind, key))
		if !ok {
			t.Fatal("not found")
		}
		if !bytes.Equal(e.Result, result) || e.Error != anError.Error() {
			t.Errorf("got (%q, %q), want (%q, %q)", e.Result, e.Error, result, anError)
		}
	})
}

func compareSlices[T any](t *testing.T, got, want []T, eq func(T, T) bool) {
	t.Helper()
	for i := range max(len(got), len(want)) {
		if i >= len(got) {
			t.Errorf("%d: missing got", i)
		} else if i >= len(want) {
			t.Errorf("%d: missing want", i)
		} else if !eq(got[i], want[i]) {
			t.Errorf("%d:\ngot  %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

func TestApproved(t *testing.T) {
	approve := Decision{Name: "n", Time: time.Now(), Approved: true}
	deny := Decision{Name: "n", Time: time.Now(), Approved: false}
	for _, test := range []struct {
		req  bool
		ds   []Decision
		want bool
	}{
		{false, nil, true},              // approval not required => approved
		{false, []Decision{deny}, true}, // ...even if there are denials.
		{true, nil, false},
		{true, []Decision{approve}, true},
		{true, []Decision{approve, approve}, true},
		{true, []Decision{approve, deny, approve}, false}, // denials have veto power
	} {
		e := &Entry{
			ApprovalRequired: test.req,
			Decisions:        test.ds,
		}
		if got := e.Approved(); got != test.want {
			t.Errorf("%+v: got %t, want %t", e, got, test.want)
		}
	}
}

func TestRun(t *testing.T) {
	ctx := context.Background()
	const actionKind = "akind"
	key := ordered.Encode("key")
	var errAction = errors.New("action failed")
	lg := testutil.Slogger(t)
	nRunCalls := 0

	before := Register(actionKind, testActioner{
		run: func(_ context.Context, action []byte) ([]byte, error) {
			nRunCalls++
			if string(action) == "fail" {
				return nil, errAction
			}
			return append([]byte("result "), action...), nil
		},
	})

	t.Run("basic", func(t *testing.T) {
		db := storage.MemDB()
		actions := []string{"a1", "a2", "fail"}
		for i, a := range actions {
			before(db, ordered.Encode(i), []byte(a), !RequiresApproval)
		}

		err := Run(ctx, lg, db)
		// Expect one error, the failed action.
		errs := err.(interface{ Unwrap() []error }).Unwrap()
		if len(errs) != 1 || errs[0] != errAction {
			t.Fatalf("wanted one errAction, got %+v", errs)
		}

		// There should be no pending actions.
		for range timed.ScanAfter(lg, db, pendingKind, 0, nil) {
			t.Fatal("there are still pending actions")
		}

		// The log should contain all the executed actions and their results.
		var want []*Entry
		for i := range len(actions) {
			want = append(want, &Entry{
				Key:    ordered.Encode(i),
				Action: []byte(actions[i]),
				Result: []byte("result " + actions[i]),
				Error:  "",
			})
		}
		want[2].Result = nil
		want[2].Error = "action failed"
		got := slices.Collect(ScanAfter(lg, db, time.Time{}, nil))
		compareSlices(t, got, want, func(g, w *Entry) bool {
			return bytes.Equal(g.Key, w.Key) &&
				bytes.Equal(g.Action, w.Action) &&
				!g.Done.IsZero() &&
				bytes.Equal(g.Result, w.Result) &&
				g.Error == w.Error &&
				!g.ApprovalRequired &&
				len(g.Decisions) == 0
		})
	})
	t.Run("actions are run only once", func(t *testing.T) {
		check := testutil.Checker(t)
		nRunCalls = 0
		db := storage.MemDB()
		before(db, key, nil, !RequiresApproval)
		check(Run(ctx, lg, db))
		check(Run(ctx, lg, db))
		if nRunCalls != 1 {
			t.Fatalf("got %d calls, want 1", nRunCalls)
		}
		e, ok := Get(db, actionKind, key)
		if !ok || !e.IsDone() {
			t.Fatal("entry not done")
		}
	})
	t.Run("(un)approved actions are (not) run", func(t *testing.T) {
		check := testutil.Checker(t)
		nRunCalls = 0
		db := storage.MemDB()

		checkRunAndDone := func(key []byte, wantRun bool) {
			t.Helper()
			check(Run(ctx, lg, db))
			wantN := 0
			wantDone := false
			if wantRun {
				wantN = 1
				wantDone = true
			}
			if nRunCalls != wantN {
				t.Errorf("got %d calls, want %d", nRunCalls, wantN)
			}
			e, ok := Get(db, actionKind, key)
			if !ok {
				t.Fatal("action not found")
			}
			if got := e.IsDone(); got != wantDone {
				t.Errorf("done = %t, want %t", got, wantDone)
			}
		}

		// unapproved, not run
		before(db, key, nil, RequiresApproval)
		checkRunAndDone(key, false)

		// denied, still not run
		AddDecision(db, actionKind, key, Decision{Approved: false})
		checkRunAndDone(key, false)

		// denied and approved, still not run
		AddDecision(db, actionKind, key, Decision{Approved: true})
		checkRunAndDone(key, false)

		// approved, run
		// We can't remove a decision, so make a new action.
		key2 := ordered.Encode("key2")
		before(db, key2, nil, RequiresApproval)
		AddDecision(db, actionKind, key2, Decision{Approved: true})
		checkRunAndDone(key2, true)
	})

	t.Run("WithReport", func(t *testing.T) {
		db := storage.MemDB()
		before(db, ordered.Encode(0), []byte("a1"), !RequiresApproval)
		before(db, ordered.Encode(1), []byte("a2"), RequiresApproval)
		before(db, ordered.Encode(2), []byte("fail"), !RequiresApproval)

		got := RunWithReport(ctx, lg, db)
		want := &RunReport{
			Completed: 1,
			Skipped:   1,
			Errors:    []error{errAction},
		}
		if !gcmp.Equal(got, want, cmpopts.EquateErrors()) {
			t.Errorf("RunWithReport = %+v, want %+v", got, want)
		}
	})
}

func TestReRunAction(t *testing.T) {
	ctx := context.Background()
	const actionKind = "bkind"
	db := storage.MemDB()
	lg := testutil.Slogger(t)

	called := false
	Register(actionKind, testActioner{
		run: func(_ context.Context, action []byte) ([]byte, error) {
			called = true
			return nil, nil
		},
	})

	for i, tc := range []struct {
		entry     entry
		wantError string
	}{
		{
			entry: entry{
				Done:             time.Time{},
				Error:            "",
				ApprovalRequired: false,
			},
			wantError: "not done",
		},
		{
			entry: entry{
				Done:             time.Time{},
				Error:            "",
				ApprovalRequired: true,
			},
			wantError: "not approved",
		},
		{
			entry: entry{
				Done:             time.Now(),
				Error:            "",
				ApprovalRequired: false,
			},
			wantError: "did not fail",
		},
		{
			entry: entry{
				Done:             time.Now(),
				Error:            "bad stuff",
				ApprovalRequired: false,
			},
			wantError: "",
		},
	} {
		tc.entry.Created = time.Now()
		tc.entry.Kind = actionKind
		tc.entry.Key = []byte(strconv.Itoa(i))
		setEntry(db, dbKey(actionKind, tc.entry.Key), &tc.entry)
		called = false

		err := ReRunAction(ctx, lg, db, tc.entry.Kind, tc.entry.Key)
		if err != nil {
			if tc.wantError == "" {
				t.Errorf("%+v: unexpected error: %v", tc.entry, err)
			} else if !strings.Contains(err.Error(), tc.wantError) {
				t.Errorf("%+v: got error %q, wanted it to contain %q", tc.entry, err, tc.wantError)
			}
			continue
		}
		if tc.wantError != "" {
			t.Errorf("%+v: got no error, expected one", tc.entry)
			continue
		}
		if !called {
			t.Errorf("%+v: runAction not called, but should have been", tc.entry)
		}
	}
}

type testActioner struct {
	Actioner
	run func(context.Context, []byte) ([]byte, error)
}

func (t testActioner) Run(ctx context.Context, data []byte) ([]byte, error) {
	return t.run(ctx, data)
}

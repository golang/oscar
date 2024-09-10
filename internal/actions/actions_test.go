// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package actions

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"golang.org/x/oscar/internal/storage"
	"rsc.io/ordered"
)

func TestDB(t *testing.T) {
	var (
		namespace = "test"
		key       = ordered.Encode("num", 23)
		action    = []byte("action")
		result    = []byte("result")
		error     = errors.New("bad")
	)
	t.Run("before-after", func(t *testing.T) {
		db := storage.MemDB()
		dkey := before(db, namespace, key, action, false)
		e, ok := get(db, dkey)
		if !ok {
			t.Fatal("not found")
		}
		var unique uint64
		if err := ordered.Decode(dkey, nil, nil, nil, &unique); err != nil {
			t.Fatal(err)
		}
		want := &Entry{
			Created:          e.Created,
			Namespace:        namespace,
			Key:              key,
			Unique:           unique,
			Action:           action,
			ApprovalRequired: false,
			ModTime:          e.ModTime,
		}
		if !reflect.DeepEqual(e, want) {
			t.Errorf("Before:\ngot  %+v\nwant %+v", e, want)
		}

		after(db, dkey, result, error)
		e, ok = get(db, dkey)
		if !ok {
			t.Fatal("not found")
		}
		want.Done = e.Done
		want.ModTime = e.ModTime
		want.Result = result
		want.Error = "bad"
		if !reflect.DeepEqual(e, want) {
			t.Errorf("After:\ngot  %+v\nwant %+v", e, want)
		}
	})
	t.Run("approval", func(t *testing.T) {
		db := storage.MemDB()
		dkey := before(db, namespace, key, action, true)
		var u uint64
		if err := ordered.Decode(dkey, nil, nil, nil, &u); err != nil {
			t.Fatal(err)
		}
		tm := time.Now().Round(0).In(time.UTC)
		d1 := Decision{Name: "name1", Time: tm, Approved: true}
		d2 := Decision{Name: "name2", Time: tm, Approved: false}
		AddDecision(db, namespace, key, u, d1)
		AddDecision(db, namespace, key, u, d2)
		e, ok := Get(db, namespace, key, u)
		if !ok {
			t.Fatal("not found")
		}
		want := &Entry{
			Created:          e.Created,
			ModTime:          e.ModTime,
			Namespace:        namespace,
			Key:              key,
			Unique:           u,
			Action:           action,
			ApprovalRequired: true,
			Decisions:        []Decision{d1, d2},
		}
		if !reflect.DeepEqual(e, want) {
			t.Errorf("\ngot:  %+v\nwant: %+v", e, want)
		}
	})
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

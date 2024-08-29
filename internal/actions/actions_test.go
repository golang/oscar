// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package actions

import (
	"errors"
	"reflect"
	"testing"

	"golang.org/x/oscar/internal/storage"
	"rsc.io/ordered"
)

func TestBeforeAfter(t *testing.T) {
	db := storage.MemDB()
	var (
		namespace = "test"
		key       = ordered.Encode("num", 23)
		action    = []byte("action")
		result    = []byte("result")
		error     = errors.New("bad")
	)
	dkey := before(db, namespace, key, action, true)
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
		ApprovalRequired: true,
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
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package firestore

import (
	"context"
	"fmt"
	"testing"

	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
)

// To record this test:
//
//	go test -v -run 'TestVectorDB$' -grpcrecord vectordb -project $OSCAR_PROJECT -database test
func TestVectorDB(t *testing.T) {
	rr, project, database := openRR(t, "testdata/vectordb.grpcrr")
	ctx := context.Background()
	nsid := 0
	newvdb := func() storage.VectorDB {
		ns := fmt.Sprintf("namespace-%d", nsid)
		// TODO: use a different namespace each time
		// nsid++
		vdb, err := NewVectorDB(ctx, testutil.Slogger(t), project, database, ns, rr.ClientOptions()...)
		if err != nil {
			t.Fatal(err)
		}
		return vdb
	}
	storage.TestVectorDB(t, newvdb)
}

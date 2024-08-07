// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package firestore

import (
	"context"
	"testing"

	"cloud.google.com/go/firestore"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
	"google.golang.org/api/iterator"
)

// To record this test:
//
//	go test -v -run 'TestVectorDB$' -grpcrecord vectordb -project $OSCAR_PROJECT -database test
func TestVectorDB(t *testing.T) {
	rr, project, database := openRR(t, "testdata/vectordb.grpcrr")
	ctx := context.Background()
	if rr.Recording() {
		deleteVectorDBs(t, project, database)
	}
	storage.TestVectorDB(t, func() storage.VectorDB {
		vdb, err := NewVectorDB(ctx, testutil.Slogger(t), project, database, "test", rr.ClientOptions()...)
		if err != nil {
			t.Fatal(err)
		}
		return vdb
	})
}

func deleteVectorDBs(t *testing.T, project, database string) {
	ctx := context.Background()
	client, err := firestore.NewClientWithDatabase(ctx, project, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	bw := client.BulkWriter(ctx)
	// Iterate over all "vectors" collections, regardless of namespace.
	// These collections are the immediate parents of the vector documents,
	// which are what we want to delete.
	iter := client.CollectionGroup("vectors").Documents(ctx)
	for {
		ds, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		bw.Delete(ds.Ref)
	}
	bw.Flush()
}

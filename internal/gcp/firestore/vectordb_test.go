// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package firestore

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"golang.org/x/oscar/internal/gcp/grpcerrors"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/testutil"
	"google.golang.org/api/iterator"
)

func TestVectorDB(t *testing.T) {
	rr, project := openRR(t, "testdata/vectordb.grpcrr")
	ctx := context.Background()
	if rr.Recording() {
		deleteVectorDBs(t, project, firestoreTestDatabase)
	}
	storage.TestVectorDB(t, func() storage.VectorDB {
		vdb, err := NewVectorDB(ctx, testutil.Slogger(t), project, firestoreTestDatabase, "test", rr.ClientOptions()...)
		if err != nil {
			t.Fatal(err)
		}
		return vdb
	})
}

func TestVectorDBSlow(t *testing.T) {
	// Re-record with
	//	OSCAR_PROJECT=oscar-go-1 go test -v -timeout=1h -run=TestVectorDBSlow -grpcrecord=/vectordbslow
	rr, project := openRR(t, "testdata/vectordbslow.grpcrr.gz")
	ctx := context.Background()
	if rr.Recording() {
		deleteVectorDBs(t, project, firestoreTestDatabase)
	}

	vdb, err := NewVectorDB(ctx, testutil.Slogger(t), project, firestoreTestDatabase, "test", rr.ClientOptions()...)
	if err != nil {
		t.Fatal(err)
	}
	defer vdb.Close()

	const slowN = 5000
	b := vdb.Batch()
	slowKey := func(n int) string {
		// The Firestore limit for key names is 1500 bytes
		// but that includes the whole key path.
		// Make the key big to reduce the number of keys we need
		// to create to fill a Scan result,
		// since we can't make the data values any bigger.
		// Since we hex-encode keys, our limit is more like 750 bytes.
		return fmt.Sprintf("slow.%s.%09d", strings.Repeat("A", 730), n)
	}
	const vecSize = 16 // matches storage.TestVectorDB and test database index config
	t.Logf("creating %d keys", slowN)
	for i := range slowN {
		b.Set(slowKey(i), make(llm.Vector, vecSize))
		b.MaybeApply()
	}
	b.Apply()

	n := 0
	t.Logf("scan all")
	for k := range vdb.All() {
		if string(k) != slowKey(n) {
			t.Fatalf("slow read #%d: have %q want %q", n, k, slowKey(n))
		}
		if rr.Recording() && n == 0 {
			t.Logf("scan %s; sleep", k)
			time.Sleep(90 * time.Second)
		}
		n++
	}
	if n != slowN {
		t.Errorf("slow reads: scanned %d keys, want %d", n, slowN)
	}
}

// deleteVectorDBs deletes all the vectors and their related collections from the
// given Firestore DB, specified as a project and database name.
func deleteVectorDBs(t *testing.T, project, database string) {
	// Delete all documents in all collections named "vectors".
	// Although these all live under the "vectorDBs" collection, it isn't possible
	// to delete that collection. In Firestore, only documents can be deleted,
	// and they can only be iterated over from their immediate parent collection.
	// The CollectionGroup call selects all collections named "vectors", regardless
	// of their parents. (There is a way to recursively walk the hierarchy, but using
	// a collection group is simpler.)
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
Restart:
	iter := client.CollectionGroup("vectors").Documents(ctx)
	last := ""
	for {
		ds, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if grpcerrors.IsUnavailable(err) && last != "" {
			t.Logf("vector delete timeout; restarting; last=%s", decodeKey(last))
			goto Restart
		}
		if err != nil {
			t.Fatal(err)
		}
		bw.Delete(ds.Ref)
		last = ds.Ref.ID
	}
	bw.Flush()
}

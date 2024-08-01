// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package firestore

import (
	"context"
	"testing"

	"golang.org/x/oscar/internal/storage"
)

func TestVectorDB(t *testing.T) {
	rr, project, database := openRR(t, "testdata/vectordb.grpcrr")
	ctx := context.Background()
	storage.TestVectorDB(t, func() storage.VectorDB {
		vdb, err := NewVectorDB(ctx, &DBOptions{ProjectID: project, Database: database, ClientOptions: rr.ClientOptions()})
		if err != nil {
			t.Fatal(err)
		}
		return vdb
	})
}

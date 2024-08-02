// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package firestore

import (
	"context"
	"encoding/hex"
	"log/slog"
	"path"

	"cloud.google.com/go/firestore"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// A VectorDB is a [storage.VectorDB] using Firestore.
type VectorDB struct {
	fs        *fstore
	namespace string
	coll      *firestore.CollectionRef
}

// NewVectorDB creates a [VectorDB] with the given logger, GCP project ID,
// Firestore database, namespace and client options.
// The projectID must not be empty.
// If the database is empty, the default database will be used.
// The namespace must be a [valid Firestore collection ID].
// Namespaces allow multiple vector DBs to be stored in the same Firestore DB.
//
// Vectors in a VectorDB with namespace NS are stored in the Firestore collection
// "vectorDBs/NS/vectors".
//
// [valid Firestore collection ID]: https://firebase.google.com/docs/firestore/quotas#collections_documents_and_fields
func NewVectorDB(ctx context.Context, lg *slog.Logger, projectID, database, namespace string, opts ...option.ClientOption) (*VectorDB, error) {
	fs, err := newFstore(ctx, lg, projectID, database, opts)
	if err != nil {
		return nil, err
	}
	coll := fs.client.Collection(path.Join("vectorDBs", namespace, "vectors"))
	return &VectorDB{fs, namespace, coll}, nil
}

// Close closes the [VectorDB], releasing its resources.
func (db *VectorDB) Close() {
	db.fs.Close()
}

// A vectorDoc holds an embedding as the Firestore type for a
// vector of float32s.
// All Firestore documents must be sets of key-value pairs (structs or maps in Go),
// so we cannot represent an embedding as a lone firestore.Vector32.
// Firestore's vector DB search requires that the type of the vector is either
// firestore.Vector32 or firestore.Vector64.
type vectorDoc struct {
	Embedding firestore.Vector32
}

// Set implements [storage.VectorDB.Set].
func (db *VectorDB) Set(id string, vec llm.Vector) {
	doc := vectorDoc{firestore.Vector32(vec)}
	if _, err := db.docref(id).Set(context.TODO(), doc); err != nil {
		db.fs.Panic("firestore VectorDB Set", "id", id, "err", err)
	}
}

// Get implements [storage.VectorDB.Get].
func (db *VectorDB) Get(id string) (llm.Vector, bool) {
	docsnap, err := db.docref(id).Get(context.TODO())
	if err != nil {
		if isNotFound(err) {
			return nil, false
		}
		db.fs.Panic("firestore VectorDB Get", "id", id, "err", err)
	}
	var doc vectorDoc
	if err := docsnap.DataTo(&doc); err != nil {
		db.fs.Panic("firestore VectorDB Get", "id", id, "err", err)
	}
	return llm.Vector(doc.Embedding), true
}

// Search implements [storage.VectorDB.Search].
func (db *VectorDB) Search(vec llm.Vector, n int) []storage.VectorResult {
	q := db.coll.FindNearest("Embedding", firestore.Vector32(vec), n, firestore.DistanceMeasureDotProduct, nil)
	iter := q.Documents(context.TODO())
	defer iter.Stop()
	var res []storage.VectorResult
	for {
		docsnap, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			db.fs.Panic("firestore VectorDB Search", "err", err)
		}
		var doc vectorDoc
		if err := docsnap.DataTo(&doc); err != nil {
			db.fs.Panic("firestore VectorDB Search", "err", err)
		}
		id := db.decodeVectorID(docsnap.Ref.ID)
		res = append(res, storage.VectorResult{
			ID:    string(id),
			Score: vec.Dot(llm.Vector(doc.Embedding)),
		})
	}
	return res
}

// docref returns a DocumentReference for the document with the given ID.
func (db *VectorDB) docref(id string) *firestore.DocumentRef {
	// To avoid running into the restrictions on Firestore document IDs, escape the id.
	return db.coll.Doc(encodeVectorID(id))
}

// Flush implements [storage.VectorDB.Flush]. It is a no-op.
func (db *VectorDB) Flush() {
	// Firestore operations do not require flushing.
}

type vBatch struct {
	b *batch
}

// Batch implements [storage.VectorDB.Batch].
func (db *VectorDB) Batch() storage.VectorBatch {
	return &vBatch{db.fs.newBatch(db.coll)}
}

// Approximate size of a float64 encoded as a Firestore value.
// (Firestore encodes a float32 as a float64.)
const perFloatSize = 11

// Set implements [storage.VectorBatch.Set].
func (b *vBatch) Set(id string, vec llm.Vector) {
	b.b.set(encodeVectorID(id), vectorDoc{firestore.Vector32(vec)}, len(vec)*perFloatSize)
}

// MaybeApply implements [storage.VectorBatch.MaybeApply].
func (b *vBatch) MaybeApply() bool { return b.b.maybeApply() }

// Apply implements [storage.VectorBatch.Apply].
func (b *vBatch) Apply() { b.b.apply() }

func encodeVectorID(id string) string {
	return hex.EncodeToString([]byte(id))
}

func (db *VectorDB) decodeVectorID(id string) string {
	bid, err := hex.DecodeString(id)
	if err != nil {
		db.fs.Panic("firestore VectorDB ID decode", "id", id, "err", err)
	}
	return string(bid)
}

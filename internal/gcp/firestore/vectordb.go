// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package firestore

import (
	"context"
	"net/url"

	"cloud.google.com/go/firestore"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/storage"
	"google.golang.org/api/iterator"
)

// A VectorDB is a [storage.VectorDB] using Firestore.
type VectorDB struct {
	fs *fstore
}

// NewVectorDB creates a [VectorDB] with the given [DBOptions].
// Vectors are stored in the Firestore collection "vectors".
func NewVectorDB(ctx context.Context, dbopts *DBOptions) (*VectorDB, error) {
	fs, err := newFstore(ctx, dbopts)
	if err != nil {
		return nil, err
	}
	return &VectorDB{fs}, nil
}

// Close closes the [VectorDB], releasing its resources.
func (db *VectorDB) Close() {
	db.fs.Close()
}

const vectorCollection = "vectors"

// A vectorDoc holds an embedding as the Firestore type for a
// vector of float32s.
// All Firestore documents must be sets of key-value pairs (structs or maps in Go),
// so we cannot represent an embedding as a lone firestore.Vector32.
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
	coll := db.fs.client.Collection(vectorCollection)
	q := coll.FindNearest("Embedding", firestore.Vector32(vec), n, firestore.DistanceMeasureDotProduct, nil)
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
		id, err := url.PathUnescape(docsnap.Ref.ID)
		if err != nil {
			db.fs.Panic("firestore VectorDB Search unescape", "id", docsnap.Ref.ID, "err", err)
		}
		res = append(res, storage.VectorResult{
			ID:    id,
			Score: vec.Dot(llm.Vector(doc.Embedding)),
		})
	}
	return res
}

// docref returns a DocumentReference for the document with the given ID.
func (db *VectorDB) docref(id string) *firestore.DocumentRef {
	// Firestore document IDs cannot contain slashes, so escape the ID.
	return db.fs.client.Collection(vectorCollection).Doc(url.PathEscape(id))
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
	return &vBatch{db.fs.newBatch(vectorCollection)}
}

// Approximate size of a float64 encoded as a Firestore value.
// (Firestore encodes a float32 as a float64.)
const perFloatSize = 11

// Set implements [storage.VectorBatch.Set].
func (b *vBatch) Set(id string, vec llm.Vector) {
	b.b.set(id, vectorDoc{firestore.Vector32(vec)}, len(vec)*perFloatSize)
}

// MaybeApply implements [storage.VectorBatch.MaybeApply].
func (b *vBatch) MaybeApply() bool { return b.b.maybeApply() }

// Apply implements [storage.VectorBatch.Apply].
func (b *vBatch) Apply() { b.b.apply() }

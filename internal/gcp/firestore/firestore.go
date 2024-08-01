// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package firestore implements [storage.DB] and [storage.VectorDB]
// using Google Cloud's [Firestore] service.
//
// A brief introduction to Firestore: it is a document DB with hierarchical keys.
// A key is a string of the form "coll1/id1/coll2/id2/.../collN/idN",
// where the colls are called "collections" and the values are called "documents".
// Each document is a set of key-value pairs. In Go, a document is represented
// by a map[string]any, or a struct with exported fields:
// the Go Firestore client provides convenience functions for converting documents to and from structs.
//
// The two database implementations in this package use three collections:
//   - The "locks" collection holds documents representing the locks used by [DB.Lock] and [DB.Unlock].
//   - The "values" collection holds the key-value pairs for [DB]. Keys are byte slices but document
//     names are strings. We hex-encode the keys to obtain strings with the same ordering.
//   - The "vectors" collection holds embeddding vectors for [VectorDB].
//
// [Firestore]: https://cloud.google.com/firestore
package firestore

import (
	"context"
	"iter"
	"log/slog"
	"reflect"

	"cloud.google.com/go/firestore"
	"golang.org/x/oscar/internal/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DBOptions is the configuration for a [DB] or [VectorDB].
type DBOptions struct {
	ProjectID     string // if empty, attempt to detect the project
	Database      string // if empty, use the default database
	Logger        *slog.Logger
	ClientOptions []option.ClientOption // options to the Firestore client
}

// fstore implements operations common to both [storage.DB] and [storage.VectorDB].
type fstore struct {
	client *firestore.Client
	slog   *slog.Logger
}

func newFstore(ctx context.Context, dbopts *DBOptions) (*fstore, error) {
	if dbopts == nil {
		dbopts = &DBOptions{}
	}
	if dbopts.ProjectID == "" {
		dbopts.ProjectID = firestore.DetectProjectID
	}
	if dbopts.Database == "" {
		dbopts.Database = firestore.DefaultDatabaseID
	}
	if dbopts.Logger == nil {
		dbopts.Logger = slog.Default()
	}
	client, err := firestore.NewClientWithDatabase(ctx, dbopts.ProjectID, dbopts.Database, dbopts.ClientOptions...)
	if err != nil {
		return nil, err
	}
	// The client doesn't actually connect until something is done, so get a document
	// to see if there's a problem.
	if _, err := client.Doc("c/d").Get(ctx); err != nil && !isNotFound(err) {
		return nil, err
	}
	return &fstore{client: client, slog: dbopts.Logger}, nil
}

func (f *fstore) Flush() {
	// Firestore operations do not require flushing.
}

func (f *fstore) Close() {
	if err := f.client.Close(); err != nil {
		// unreachable except for bad DB
		f.Panic("firestore close", "err", err)
	}
}

func (f *fstore) Panic(msg string, args ...any) {
	f.slog.Error(msg, args...)
	storage.Panic(msg, args...)
}

// get retrieves the document with the given collection and ID.
// If tx is non-nil, the get happens inside the transaction.
func (f *fstore) get(tx *firestore.Transaction, coll, id string) *firestore.DocumentSnapshot {
	dr := f.client.Collection(coll).Doc(id)
	var ds *firestore.DocumentSnapshot
	var err error
	if tx == nil {
		ds, err = dr.Get(context.TODO())
	} else {
		ds, err = tx.Get(dr)
	}
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		// unreachable except for bad DB
		f.Panic("firestore get", "collection", coll, "key", id, "err", err)
	}
	return ds
}

// set sets the document with the given collection and ID to value.
// If tx is non-nil, the set happens inside the transaction.
func (f *fstore) set(tx *firestore.Transaction, coll, key string, value any) {
	dr := f.client.Collection(coll).Doc(key)
	if dr == nil {
		f.Panic("firestore set bad doc ref args", "collection", coll, "key", key)
	}
	var err error
	if tx == nil {
		_, err = dr.Set(context.TODO(), value)
	} else {
		err = tx.Set(dr, value)
	}
	if err != nil {
		// unreachable except for bad DB
		f.Panic("firestore set", "collection", coll, "key", key, "err", err)
	}
}

// delete deletes the document with the given collection and ID.
// If tx is non-nil, the delete happens inside the transaction.
// It is not an error to call delete on a document that doesn't exist.
func (f *fstore) delete(tx *firestore.Transaction, coll, key string) {
	dr := f.client.Collection(coll).Doc(key)
	var err error
	if tx == nil {
		_, err = dr.Delete(context.TODO())
	} else {
		err = tx.Delete(dr)
	}
	if err != nil {
		// unreachable except for bad DB
		f.Panic("firestore delete", "collection", coll, "key", key, "err", err)
	}
}

// deleteRange deletes all the documents in the collection coll whose IDs are between
// start and end, inclusive.
func (f *fstore) deleteRange(coll, start, end string) {
	bw := f.client.BulkWriter(context.TODO())
	for ds := range f.scan(nil, coll, start, end) {
		if _, err := bw.Delete(ds.Ref); err != nil {
			// unreachable except for bad DB
			f.Panic("firestore delete range", "collection", coll, "err", err)
		}
	}
	bw.End()
}

// runTransaction executes fn inside a transaction.
func (f *fstore) runTransaction(fn func(ctx context.Context, tx *firestore.Transaction)) {
	err := f.client.RunTransaction(context.TODO(), func(ctx context.Context, tx *firestore.Transaction) error {
		fn(ctx, tx)
		return nil
	})
	if err != nil {
		// unreachable except for bad DB
		f.Panic("firestore transaction", "err", err)
	}
}

// scan returns an iterator over the documents in the collection coll whose IDs are
// between start and end, inclusive.
func (f *fstore) scan(tx *firestore.Transaction, coll, start, end string) iter.Seq[*firestore.DocumentSnapshot] {
	query := f.client.Collection(coll).
		OrderBy(firestore.DocumentID, firestore.Asc).
		StartAt(start).
		EndAt(end)
	var iter *firestore.DocumentIterator
	if tx == nil {
		iter = query.Documents(context.TODO())
	} else {
		iter = tx.Documents(query)
	}
	return func(yield func(*firestore.DocumentSnapshot) bool) {
		defer iter.Stop()
		for {
			ds, err := iter.Next()
			if err == iterator.Done {
				return
			}
			if err != nil {
				// unreachable except for bad DB
				f.Panic("firestore scan", "collection", coll, "err", err)
			}
			if !yield(ds) {
				return
			}
		}
	}
}

// dataTo converts the data in the given DocumentSnapshot to a value of type T.
func dataTo[T any](f *fstore, ds *firestore.DocumentSnapshot) T {
	var data T
	if err := ds.DataTo(&data); err != nil {
		// unreachable except for bad DB
		f.Panic("dataTo", "type", reflect.TypeFor[T](), "err", err)
	}
	return data
}

// batch implements [storage.Batch].
// All of a batch's operations must occur within the same collection.
type batch struct {
	f              *fstore
	coll           string
	ops            []*op
	size           int  // approximate size of ops
	hasDeleteRange bool // at least one op is a deleteRange
}

func (f *fstore) newBatch(coll string) *batch {
	return &batch{f: f, coll: coll, size: fixedSize}
}

// An op is a single batch operation.
type op struct {
	id    string // or start, for deleteRange
	value any    // nil for delete, deleteRange
	// for deleteRange:
	end       string
	deleteIDs []string // the list of IDs to delete, determined inside the transaction
}

// Approximate proto sizes, determined empirically.
const (
	fixedSize    = 300
	perWriteSize = 5
	// Firestore supposedly has a limit of 10 MiB per request
	// (see https://cloud.google.com/firestore/quotas#writes_and_transactions).
	// However, I found that exceeding 4 MiB often failed
	// with a "too big" error.
	maxSize = 4 << 20
)

// set adds a set operation to the batch. It is the caller's responsibility to
// estimate the size of val.
func (b *batch) set(id string, val any, valSize int) {
	if val == nil {
		b.f.Panic("firestore batch set: nil value")
	}
	b.ops = append(b.ops, &op{id: id, value: val})
	b.size += perWriteSize + len(id) + valSize
}

// delete adds a delete operation to the batch.
func (b *batch) delete(id string) {
	b.ops = append(b.ops, &op{id: id, value: nil})
	b.size += perWriteSize + len(id)
}

// delete adds a deleteRange operation to the batch.
func (b *batch) deleteRange(start, end string) {
	b.ops = append(b.ops, &op{id: start, end: end})
	b.size += perWriteSize + len(start) + len(end)
	b.hasDeleteRange = true
}

// maybeApply applies the batch if it is big enough.
func (b *batch) maybeApply() bool {
	// Apply if the batch is large, or if there is even one deleteRange.
	// We don't know how many documents are in the range, and each one
	// is a separate operation in the transaction, so be conservative.
	if b.size > maxSize || b.hasDeleteRange {
		b.apply()
		return true
	}
	return false
}

// apply applies the batch by executing all its operations in a transaction.
func (b *batch) apply() {
	b.f.runTransaction(func(ctx context.Context, tx *firestore.Transaction) {
		// Reads must happen before writes in a transaction, and we can only delete
		// a range by querying the documents.
		// So read all the ranges first, and store the IDs in their respective ops
		// to preserve deletion order.
		for _, op := range b.ops {
			if op.end != "" {
				for ds := range b.f.scan(tx, b.coll, op.id, op.end) {
					op.deleteIDs = append(op.deleteIDs, ds.Ref.ID)
				}
			}
		}

		// Execute each op inside the transaction.
		for _, op := range b.ops {
			switch {
			case op.value != nil: // set
				b.f.set(tx, b.coll, op.id, op.value)
			case op.end == "": // delete
				b.f.delete(tx, b.coll, op.id)
			default: // deleteRange
				for _, id := range op.deleteIDs {
					b.f.delete(tx, b.coll, id)
				}
			}
		}
	})
	b.ops = nil
	b.size = fixedSize
}

// isNotFound reports whether an error returned by the Firestore client is a NotFound
// error.
func isNotFound(err error) bool {
	return status.Code(err) == codes.NotFound
}

// isTimeout reports whether an error returned by the Firestore client indicates a timeout.
func isTimeout(err error) bool {
	return status.Code(err) == codes.DeadlineExceeded
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package storage

import (
	"cmp"
	"iter"

	"golang.org/x/oscar/internal/llm"
)

// A VectorDB is a vector database that implements
// nearest-neighbor search over embedding vectors
// corresponding to documents.
type VectorDB interface {
	// Set sets the vector associated with the given document ID to vec.
	Set(id string, vec llm.Vector)

	// Delete deletes any vector associated with document ID key.
	// Delete of an unset key is a no-op.
	Delete(id string)

	// Get gets the vector associated with the given document ID.
	// If no such document exists, Get returns nil, false.
	// If a document exists, Get returns vec, true.
	Get(id string) (llm.Vector, bool)

	// All returns an iterator over all ID-vector pairs in the vector db.
	// The second value in each iteration pair is a function returning a
	// vector, not the vector itself:
	//
	//	for key, getVec := range vecdb.All() {
	//		vec := getVec()
	//		fmt.Printf("%q: %q\n", key, vec)
	//	}
	//
	// In iterations that only need the keys or only need the vectors for a subset of keys,
	// some VectorDB implementations may avoid work when the value function is not called.
	All() iter.Seq2[string, func() llm.Vector]

	// Batch returns a new [VectorBatch] that accumulates
	// vector database mutations to apply in an atomic operation.
	// It is more efficient than repeated calls to Set.
	Batch() VectorBatch

	// Search searches the database for the n vectors
	// most similar to vec, returning the document IDs
	// and similarity scores.
	//
	// Normally a VectorDB is used entirely with vectors of a single length.
	// Search ignores stored vectors with a different length than vec.
	Search(vec llm.Vector, n int) []VectorResult

	// Flush flushes storage to disk.
	Flush()
}

// A VectorBatch accumulates vector database mutations
// that are applied to a [VectorDB] in a single atomic operation.
// Applying bulk operations in a batch is also more efficient than
// making individual [VectorDB] method calls.
// The batched operations apply in the order they are made.
type VectorBatch interface {
	// Set sets the vector associated with the given document ID to vec.
	Set(id string, vec llm.Vector)

	// Delete deletes any vector associated with document ID key.
	// Delete of an unset key is a no-op.
	Delete(id string)

	// MaybeApply calls Apply if the VectorBatch is getting close to full.
	// Every VectorBatch has a limit to how many operations can be batched,
	// so in a bulk operation where atomicity of the entire batch is not a concern,
	// calling MaybeApply gives the VectorBatch implementation
	// permission to flush the batch at specific “safe points”.
	// A typical limit for a batch is about 100MB worth of logged operations.
	//
	// MaybeApply reports whether it called Apply.
	MaybeApply() bool

	// Apply applies all the batched operations to the underlying VectorDB
	// as a single atomic unit.
	// When Apply returns, the VectorBatch is an empty batch ready for
	// more operations.
	Apply()
}

// A VectorResult is a single document returned by a VectorDB search.
type VectorResult struct {
	ID    string  // document ID
	Score float64 // similarity score in range [0, 1]; 1 is exact match
}

func (x VectorResult) cmp(y VectorResult) int {
	if x.Score != y.Score {
		return cmp.Compare(x.Score, y.Score)
	}
	return cmp.Compare(x.ID, y.ID)
}

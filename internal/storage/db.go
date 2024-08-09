// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package storage defines the storage abstractions needed for Oscar:
// [DB], a basic key-value store, and [VectorDB], a vector database.
// The storage needs are intentionally minimal (avoiding, for example,
// a requirement on SQL), to admit as many implementations as possible.
package storage

import (
	"encoding/json"
	"fmt"
	"iter"
	"log/slog"
	"strconv"
	"strings"

	"rsc.io/ordered"
)

// A DB is a key-value database.
//
// DB operations are assumed not to fail.
// They panic, intending to take down the program,
// if there is an error accessing the database.
// The assumption is that the program cannot possibly
// continue without the database, since that's where all the state is stored.
// Similarly, clients of DB conventionally panic
// using [DB.Panic] if the database returns corrupted data.
// Code using multiple parallel database operations can recover
// at the outermost calls.
type DB interface {
	// Lock acquires a lock on the given name, which need not exist in the database.
	// After a successful Lock(name),
	// any other call to Lock(name) from any other client of the database
	// (including in another process, for shared databases)
	// must block until Unlock(name) has been called.
	// In a shared database, a lock may also unlock
	// when the client disconnects or times out.
	Lock(name string)

	// Unlock releases the lock with the given name,
	// which the caller must have locked.
	Unlock(name string)

	// Set sets the value associated with key to val.
	// The key must not be of length zero.
	Set(key, val []byte)

	// Get looks up the value associated with key.
	// If there is no entry for key in the database, Get returns nil, false.
	// Otherwise it returns val, true.
	Get(key []byte) (val []byte, ok bool)

	// Scan returns an iterator over all key-value pairs with start ≤ key ≤ end.
	// The second value in each iteration pair is a function returning the value,
	// not the value itself:
	//
	//	for key, getVal := range db.Scan([]byte("aaa"), []byte("zzz")) {
	//		val := getVal()
	//		fmt.Printf("%q: %q\n", key, val)
	//	}
	//
	// In iterations that only need the keys or only need the values for a subset of keys,
	// some DB implementations may avoid work when the value function is not called.
	//
	// A Scan may or may not observe concurrent modifications made
	// using Set, Delete, and DeleteRange.
	Scan(start, end []byte) iter.Seq2[[]byte, func() []byte]

	// Delete deletes any value associated with key.
	// Delete of an unset key is a no-op.
	Delete(key []byte)

	// DeleteRange deletes all key-value pairs with start ≤ key ≤ end.
	DeleteRange(start, end []byte)

	// Batch returns a new [Batch] that accumulates database mutations
	// to apply in an atomic operation. In addition to the atomicity, using a
	// Batch for bulk operations is more efficient than making each
	// change using repeated calls to DB's Set, Delete, and DeleteRange methods.
	Batch() Batch

	// Flush flushes DB changes to permanent storage.
	// Flush must be called before the process crashes or exits,
	// or else any changes since the previous Flush may be lost.
	Flush()

	// Close flushes and then closes the database.
	// Like the other routines, it panics if an error happens,
	// so there is no error result.
	Close()

	// Panic logs the error message and args using the database's slog.Logger
	// and then panics with the text formatting of its arguments.
	// It is meant to be called when database corruption or other
	// database-related “can't happen” conditions have been detected.
	Panic(msg string, args ...any)
}

// A Batch accumulates database mutations that are applied to a [DB]
// as a single atomic operation. Applying bulk operations in a batch
// is also more efficient than making individual [DB] method calls.
// The batched operations apply in the order they are made.
// For example, Set("a", "b") followed by Delete("a") is the same as
// Delete("a"), while Delete("a") followed by Set("a", "b") is the same
// as Set("a", "b").
type Batch interface {
	// Delete deletes any value associated with key.
	// Delete of an unset key is a no-op.
	//
	// Delete does not retain any reference to key after returning.
	Delete(key []byte)

	// DeleteRange deletes all key-value pairs with start ≤ key ≤ end.
	//
	// DeleteRange does not retain any reference to start or end after returning.
	DeleteRange(start, end []byte)

	// Set sets the value associated with key to val.
	//
	// Set does not retain any reference to key or val after returning.
	Set(key, val []byte)

	// MaybeApply calls Apply if the batch is getting close to full.
	// Every Batch has a limit to how many operations can be batched,
	// so in a bulk operation where atomicity of the entire batch is not a concern,
	// calling MaybeApply gives the Batch implementation
	// permission to flush the batch at specific “safe points”.
	// A typical limit for a batch is about 100MB worth of logged operations.
	// MaybeApply reports whether it called Apply.
	MaybeApply() bool

	// Apply applies all the batched operations to the underlying DB
	// as a single atomic unit.
	// When Apply returns, the Batch is an empty batch ready for
	// more operations.
	Apply()
}

// Panic panics with the text formatting of its arguments.
// It is meant to be called for database errors or corruption,
// which have been defined to be impossible.
// (See the [DB] documentation.)
//
// Panic is expected to be used by DB implementations.
// DB clients should use the [DB.Panic] method instead.
func Panic(msg string, args ...any) {
	var b strings.Builder
	slog.New(slog.NewTextHandler(&b, nil)).Error(msg, args...)
	s := b.String()
	if _, rest, ok := strings.Cut(s, " level=ERROR msg="); ok {
		s = rest
	}
	panic(strings.TrimSpace(s))
}

// JSON converts x to JSON and returns the result.
// It panics if there is any error converting x to JSON.
// Since whether x can be converted to JSON depends
// almost entirely on its type, a marshaling error indicates a
// bug at the call site.
//
// (The exception is certain malformed UTF-8 and floating-point
// infinity and NaN. Code must be careful not to use JSON with those.)
func JSON(x any) []byte {
	js, err := json.Marshal(x)
	if err != nil {
		panic(fmt.Sprintf("json.Marshal: %v", err))
	}
	return js
}

// Fmt formats data for printing,
// first trying [ordered.DecodeFmt] in case data is an [ordered encoding],
// then trying a backquoted string if possible
// (handling simple JSON data),
// and finally resorting to [strconv.QuoteToASCII].
func Fmt(data []byte) string {
	if s, err := ordered.DecodeFmt(data); err == nil {
		return s
	}
	s := string(data)
	if strconv.CanBackquote(s) {
		return "`" + s + "`"
	}
	return strconv.QuoteToASCII(s)
}

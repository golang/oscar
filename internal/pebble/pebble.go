// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pebble implements a storage.DB using Pebble,
// a production-quality key-value database from CockroachDB.
// A pebble database can only be opened by one process at a time.
package pebble

import (
	"bytes"
	"cmp"
	"iter"
	"log/slog"

	"github.com/cockroachdb/pebble"
	"golang.org/x/oscar/internal/storage"
)

// Open opens an existing Pebble database in the named directory.
// The database must already exist.
func Open(lg *slog.Logger, dir string) (storage.DB, error) {
	return open(lg, dir, &pebble.Options{ErrorIfNotExists: true})
}

// Create creates a new Pebble database in the named directory.
// The database (and directory) must not already exist.
func Create(lg *slog.Logger, dir string) (storage.DB, error) {
	return open(lg, dir, &pebble.Options{ErrorIfExists: true})
}

func open(lg *slog.Logger, dir string, opts *pebble.Options) (storage.DB, error) {
	p, err := pebble.Open(dir, opts)
	if err != nil {
		lg.Error("pebble open", "dir", dir, "create", opts.ErrorIfExists, "err", err)
		return nil, err
	}
	return &db{p: p, slog: lg}, nil
}

type db struct {
	p    *pebble.DB
	m    storage.MemLocker
	slog *slog.Logger
}

type batch struct {
	db *db
	b  *pebble.Batch
}

func (d *db) Lock(key string) {
	d.m.Lock(key)
}

func (d *db) Unlock(key string) {
	d.m.Unlock(key)
}

func (d *db) get(key []byte, yield func(val []byte)) {
	v, c, err := d.p.Get(key)
	if err == pebble.ErrNotFound {
		return
	}
	if err != nil {
		// unreachable except db error
		d.Panic("pebble get", "key", storage.Fmt(key), "err", err)
	}
	yield(v)
	c.Close()
}

func (d *db) Get(key []byte) (val []byte, ok bool) {
	d.get(key, func(v []byte) {
		val = bytes.Clone(v)
		ok = true
	})
	return
}

var (
	sync   = &pebble.WriteOptions{Sync: true}
	noSync = &pebble.WriteOptions{Sync: false}
)

func (d *db) Panic(msg string, args ...any) {
	d.slog.Error(msg, args...)
	storage.Panic(msg, args...)
}

func (d *db) Set(key, val []byte) {
	if err := d.p.Set(key, val, noSync); err != nil {
		// unreachable except db error
		d.Panic("pebble set", "key", storage.Fmt(key), "val", storage.Fmt(val), "err", err)
	}
}

func (d *db) Delete(key []byte) {
	if err := d.p.Delete(key, noSync); err != nil {
		// unreachable except db error
		d.Panic("pebble delete", "key", storage.Fmt(key), "err", err)
	}
}

func (d *db) DeleteRange(start, end []byte) {
	err := cmp.Or(
		d.p.DeleteRange(start, end, noSync),
		d.p.Delete(end, noSync),
	)
	if err != nil {
		// unreachable except db error
		d.Panic("pebble delete range", "start", storage.Fmt(start), "end", storage.Fmt(end), "err", err)
	}
}

func (d *db) Flush() {
	if err := d.p.Flush(); err != nil {
		// unreachable except db error
		d.Panic("pebble flush", "err", err)
	}
}

func (d *db) Close() {
	if err := d.p.Close(); err != nil {
		// unreachable except db error
		d.Panic("pebble close", "err", err)
	}
}

func (d *db) Scan(start, end []byte) iter.Seq2[[]byte, func() []byte] {
	start = bytes.Clone(start)
	end = bytes.Clone(end)
	return func(yield func(key []byte, val func() []byte) bool) {
		// Note: Pebble's UpperBound is non-inclusive (not included in the scan)
		// but we want to include the key end in the scan,
		// so do not use UpperBound; we check during the iteration instead.
		iter, err := d.p.NewIter(&pebble.IterOptions{
			LowerBound: start,
		})
		if err != nil {
			// unreachable except db error
			d.Panic("pebble new iterator", "start", storage.Fmt(start), "err", err)
		}
		defer iter.Close()
		for iter.First(); iter.Valid(); iter.Next() {
			key := iter.Key()
			if bytes.Compare(key, end) > 0 {
				break
			}
			val := func() []byte {
				v, err := iter.ValueAndErr()
				if err != nil {
					// unreachable except db error
					d.Panic("pebble iterator value", "key", storage.Fmt(key), "err", err)
				}
				return v
			}
			if !yield(key, val) {
				return
			}
		}
	}
}

func (d *db) Batch() storage.Batch {
	return &batch{d, d.p.NewBatch()}
}

func (b *batch) Set(key, val []byte) {
	if err := b.b.Set(key, val, noSync); err != nil {
		// unreachable except db error
		b.db.Panic("pebble batch set", "key", storage.Fmt(key), "val", storage.Fmt(val), "err", err)
	}
}

func (b *batch) Delete(key []byte) {
	if err := b.b.Delete(key, noSync); err != nil {
		// unreachable except db error
		b.db.Panic("pebble batch delete", "key", storage.Fmt(key), "err", err)
	}
}

func (b *batch) DeleteRange(start, end []byte) {
	err := cmp.Or(
		b.b.DeleteRange(start, end, noSync),
		b.b.Delete(end, noSync),
	)
	if err != nil {
		// unreachable except db error
		b.db.Panic("pebble batch delete range", "start", storage.Fmt(start), "end", storage.Fmt(end), "err", err)
	}
}

// Pebble imposes a higher maximum batch size (4GB), but 100MB is fine.
// That's also what storage.Batch's interface definition says is a “typical limit”.
const maxBatch = 100e6

func (b *batch) MaybeApply() bool {
	if b.b.Len() > maxBatch {
		b.Apply()
		return true
	}
	return false
}

func (b *batch) Apply() {
	if err := b.db.p.Apply(b.b, noSync); err != nil {
		// unreachable except db error
		b.db.Panic("pebble batch apply", "err", err)
	}
	b.b.Reset()
}

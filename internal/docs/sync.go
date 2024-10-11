// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package docs

import (
	"iter"

	"golang.org/x/oscar/internal/storage/timed"
)

// Source is a data source to pull into a [Corpus].
type Source[T Entry] interface {
	// DocWatcher returns the watcher to use to keep track
	// of last [Sync] for this data source.
	DocWatcher() *timed.Watcher[T]
	// ToDocs converts the data to an iterator of [*Doc] values
	// that can be stored in a [Corpus].
	// It returns (nil, false) if the data should not be stored
	// in the [Corpus].
	ToDocs(T) (iter.Seq[*Doc], bool)
}

// Entry is a timed entry in a [Source].
type Entry interface {
	// LastWritten returns the DBTime this piece of data was last written
	// to its data source.
	LastWritten() timed.DBTime
}

// Sync reads new embeddable values from src and adds the
// documents to the corpus dc.
//
// Sync uses [Source.DocWatcher] to save its position across multiple calls.
//
// Sync logs status and unexpected problems to lg.
func Sync[T Entry, S Source[T]](dc *Corpus, src S) {
	w := src.DocWatcher()
	for e := range w.Recent() {
		ds, ok := src.ToDocs(e)
		if !ok {
			// Not embeddable, skip.
			continue
		}
		dc.slog.Debug("docs.Sync", "event", e, "dbtime", e.LastWritten())
		for d := range ds {
			dc.Add(d.ID, d.Title, d.Text)
		}
		w.MarkOld(e.LastWritten())
	}
}

// Restart causes the next call to [Sync] to behave as if
// it has never sync'ed any data before for the src.
// The result is that all data will be reconverted to doc form
// and re-added.
// Docs that have not changed since the last addition to the corpus
// will appear unmodified; others will be marked new in the corpus.
func Restart[T Entry](src Source[T]) {
	src.DocWatcher().Restart()
}

// Latest returns the latest known DBTime marked old by the source's DocWatcher.
func Latest[T Entry](src Source[T]) timed.DBTime {
	return src.DocWatcher().Latest()
}

// Latest returns a function that returns the latest known DBTime marked
// old by the source's DocWatcher.
func LatestFunc[T Entry](src Source[T]) func() timed.DBTime {
	return func() timed.DBTime { return Latest[T](src) }
}

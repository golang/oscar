// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Syncdb synchronizes one [storage.DB] with another.
It copies items (key-value pairs) in the source DB that are
not in the destination (with one exception—see below),
and removes items in the destination that are not in the source.
It assumes that the largest key in the source is ordered.Encode(ordered.Inf).

The exception is that ordered-encoded source keys beginning with "llm.Vector"
are not copied. Some DBs use these keys to represent a [storage.VectorDB], but
not all do. This tool does not sync VectorDBs.

If syncdb completes successfully and there have been no changes
to the DBs while it was running, then the DBs will have the
same items, with the above exception.

Usage:

	syncdb src dst

The databases src and dst can be specified using one of these forms:

	pebble:DIR
	   A Pebble database in the directory DIR

	firestore:PROJECT,DATABASE
	   A Firestore DB in the given GCP project and Firestore database.

# Examples

To sync a Pebble database in ~/mydb with the default Firestore database in MyProjectID:

	sync pebble:~/mydb 'firestore:MyProjectID,(default)'
*/
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"iter"
	"log"
	"log/slog"
	"os"
	"strings"

	"golang.org/x/oscar/internal/gcp/firestore"
	"golang.org/x/oscar/internal/pebble"
	"golang.org/x/oscar/internal/storage"
	"rsc.io/ordered"
)

func usage() {
	fmt.Fprintf(os.Stderr, "usage: syncdb src dst\n")
	os.Exit(2)
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("syncdb: ")
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() != 2 {
		usage()
	}
	src, err := openDB(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	dst, err := openDB(flag.Arg(1))
	if err != nil {
		log.Fatal(err)
	}
	n := syncDB(dst, src)
	log.Printf("synced %d total items", n)
}

func openDB(spec string) (storage.DB, error) {
	kind, args, _ := strings.Cut(spec, ":")
	switch kind {
	case "pebble":
		return pebble.Open(slog.Default(), args)
	case "firestore":
		proj, db, _ := strings.Cut(args, ",")
		if proj == "" || db == "" {
			return nil, fmt.Errorf("invalid DB spec %q; want 'firestore:PROJECT,DATABASE'", spec)
		}
		return firestore.NewDB(context.Background(), slog.Default(), proj, db)
	default:
		return nil, errors.New("unknown DB type")
	}
}

var llmVector = ordered.Encode("llm.Vector")

// syncDB synchronizes src and dst by making the items in dst
// be the same as the ones in src.
// It ignores items in src whose keys, when decoded with rsc.io/ordered, begin "llm.Vector".
// It returns the number of items copied or deleted.
func syncDB(dst, src storage.DB) int {
	batch := dst.Batch()

	n := 0
	inf := ordered.Encode(ordered.Inf)

	dnext, stop := iter.Pull2(dst.Scan(nil, inf))
	defer stop()
	dkey, dvalf, dok := dnext()

	for skey, svalf := range src.Scan(nil, inf) {
		// Ignore source items from vector DBs.
		if bytes.HasPrefix(skey, llmVector) {
			continue
		}
		// Delete destination items before this source key.
		for dok && bytes.Compare(dkey, skey) < 0 {
			batch.Delete(dkey)
			n++
			batch.MaybeApply()
			dkey, dvalf, dok = dnext()
		}
		// Copy the source item unless its key and value equal the destination item.
		keq := dok && bytes.Equal(dkey, skey)
		sval := svalf()
		if !keq || !bytes.Equal(sval, dvalf()) {
			batch.Set(skey, sval)
			n++
			batch.MaybeApply()
		}
		if keq {
			dkey, dvalf, dok = dnext()
		}
	}
	// All remaining destination keys are larger than the largest source key;
	// delete them.
	for dok {
		batch.Delete(dkey)
		n++
		batch.MaybeApply()
		dkey, _, dok = dnext()
	}
	batch.Apply()
	return n
}

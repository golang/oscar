// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Syncdb synchronizes one [storage.DB] with another.
It copies items (key-value pairs) in the source DB that are
not in the destination and removes items in the destination
that are not in the source. It assumes that the largest key
in the source is ordered.Encode(ordered.Inf).

Syncdb synchronizes only non-[storage.VectorDB] items by default.
Vector items are identified by the keys beginning with "llm.Vector".
Note that some DBs use these keys to represent a [storage.VectorDB],
but not all do.

With the -vec option, syncdb synchronizes only [storage.VectorDB] items.
It performs the synchronization between the two specified vector
namespaces of the source and target DBs.

If syncdb completes successfully and there have been no changes
to the DBs while it was running, then the DBs will either have
the same non-vector items or the same vector items in the provided
source and target vector namespaces.

Usage:

	syncdb [-vec] src dst

The databases src and dst can be specified using one of these forms:

	pebble:DIR[:VECNAMESPACE]
	   A Pebble database in the directory DIR

	firestore:PROJECT,DATABASE[:VECNAMESPACE]
	   A Firestore DB in the given GCP project and Firestore database.

The :VECNAMESPACE suffix can only be used with the -vec flag.
When :VECNAMESPACE is omitted, the vector namespace is taken to be the empty string.

# Examples

To sync a Pebble database in ~/mydb with the default Firestore database in MyProjectID:

	syncdb pebble:~/mydb 'firestore:MyProjectID,(default)'

To sync the "vp" vector namespace of the Pebble database with the "vf" vector namespace
of the Firestore database:

	syncdb -vec pebble:~/mydb:vp 'firestore:MyProjectID,(default):vf'
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
	"slices"
	"strings"

	"golang.org/x/oscar/internal/gcp/firestore"
	"golang.org/x/oscar/internal/pebble"
	"golang.org/x/oscar/internal/storage"
	"rsc.io/ordered"
)

func usage() {
	fmt.Fprintf(os.Stderr, "usage: syncdb [-vec] src dst\n")
	os.Exit(2)
}

func main() {
	log.SetFlags(log.Ltime)
	log.SetPrefix("syncdb: ")

	var vec bool
	flag.BoolVar(&vec, "vec", false, "synchronize vector namespaces only")
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() != 2 {
		usage()
	}

	check := func(err error) {
		if err != nil {
			log.Fatal(err)
		}
	}

	n := 0
	if vec {
		src, err := openVecDB(flag.Arg(0))
		check(err)
		dst, err := openVecDB(flag.Arg(1))
		check(err)
		n = syncVecDB(dst, src)
	} else {
		src, err := openDB(flag.Arg(0))
		check(err)
		dst, err := openDB(flag.Arg(1))
		check(err)
		n = syncDB(dst, src)
	}
	log.Printf("synced %d total items", n)
}

func openDB(spec string) (storage.DB, error) {
	kind, args, _ := strings.Cut(spec, ":")
	dbInfo, namespace, _ := strings.Cut(args, ":")

	if namespace != "" {
		return nil, fmt.Errorf("namespaces (%s) supported only in vector (-vec) mode", namespace)
	}
	switch kind {
	case "pebble":
		return pebble.Open(slog.Default(), dbInfo)
	case "firestore":
		proj, db, _ := strings.Cut(dbInfo, ",")
		if proj == "" || db == "" {
			return nil, fmt.Errorf("invalid DB spec %s:%s; want 'firestore:PROJECT,DATABASE'", kind, dbInfo)
		}
		return firestore.NewDB(context.Background(), slog.Default(), proj, db)
	default:
		return nil, errors.New("unknown DB type")
	}
}

func openVecDB(spec string) (storage.VectorDB, error) {
	kind, args, _ := strings.Cut(spec, ":")
	dbInfo, namespace, _ := strings.Cut(args, ":")

	switch kind {
	case "pebble":
		db, err := pebble.Open(slog.Default(), dbInfo)
		if err != nil {
			return nil, err
		}
		return storage.MemVectorDB(db, slog.Default(), namespace), nil
	case "firestore":
		proj, db, _ := strings.Cut(dbInfo, ",")
		if proj == "" || db == "" {
			return nil, fmt.Errorf("invalid DB spec %s:%s; want 'firestore:PROJECT,DATABASE'", kind, dbInfo)
		}
		return firestore.NewVectorDB(context.Background(), slog.Default(), proj, db, namespace)
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

	maybeApply := func(key []byte) {
		n++
		if n%1000 == 0 {
			log.Printf("synced %d items, at key %s", n, storage.Fmt(key))
		}
		batch.MaybeApply()
	}

	for skey, svalf := range src.Scan(nil, inf) {
		// Ignore source items from vector DBs.
		if bytes.HasPrefix(skey, llmVector) {
			continue
		}
		// Delete destination items before this source key.
		for dok && bytes.Compare(dkey, skey) < 0 {
			// Ignore target items from vector DBs.
			if !bytes.HasPrefix(dkey, llmVector) {
				batch.Delete(dkey)
				maybeApply(skey)
			}
			dkey, dvalf, dok = dnext()
		}
		// Copy the source item unless its key and value equal the destination item.
		keq := dok && bytes.Equal(dkey, skey)
		sval := svalf()
		if !keq || !bytes.Equal(sval, dvalf()) {
			batch.Set(skey, sval)
			maybeApply(skey)
		}
		if keq {
			dkey, dvalf, dok = dnext()
		}
	}
	// All remaining destination keys are larger than the largest source key;
	// delete them.
	for dok {
		// Ignore target items from vector DBs.
		if !bytes.HasPrefix(dkey, llmVector) {
			batch.Delete(dkey)
			maybeApply(dkey)
		}
		dkey, _, dok = dnext()
	}
	batch.Apply()
	return n
}

// syncVecDB synchronizes src and dst vector DBs by making the items in dst
// be the same as the ones in src.
// It returns the number of items copied or deleted.
func syncVecDB(dst, src storage.VectorDB) int {
	batch := dst.Batch()

	n := 0

	dnext, stop := iter.Pull2(dst.All())
	defer stop()
	dkey, dvalf, dok := dnext()

	maybeApply := func(key string) {
		n++
		if n%1000 == 0 {
			log.Printf("synced %d items, at key %s", n, key)
		}
		batch.MaybeApply()
	}

	for skey, svalf := range src.All() {
		// Delete destination items before this source key.
		for dok && dkey < skey {
			batch.Delete(dkey)
			maybeApply(skey)
			dkey, dvalf, dok = dnext()
		}
		// Copy the source item unless its key and value equal the destination item.
		keq := dok && dkey == skey
		sval := svalf()
		if !keq || !slices.Equal(sval, dvalf()) {
			batch.Set(skey, sval)
			maybeApply(skey)
		}
		if keq {
			dkey, dvalf, dok = dnext()
		}
	}
	// All remaining destination keys are larger than the largest source key;
	// delete them.
	for dok {
		batch.Delete(dkey)
		maybeApply(dkey)
		dkey, _, dok = dnext()
	}
	batch.Apply()
	return n
}

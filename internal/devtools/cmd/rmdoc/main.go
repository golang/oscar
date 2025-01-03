// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// rmdoc deletes the documents from the corpus (including the vector db).
//
//	Usage:  go run . -project oscar-go-1 -firestoredb devel https://go.dev/x/y/z
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"strings"

	"cloud.google.com/go/compute/metadata"
	"golang.org/x/oscar/internal/dbspec"
	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/gcp/firestore"
	"golang.org/x/oscar/internal/storage"
)

var flags = struct {
	project     string
	firestoredb string
	overlay     string
}{}

func init() {
	flag.StringVar(&flags.project, "project", "", "name of the Google Cloud Project")
	flag.StringVar(&flags.firestoredb, "firestoredb", "", "name of the firestore db")
}

var logger = slog.Default()

func main() {
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		log.Fatal("no args")
	}

	gabyDB, gabyVectorDB := initGCP()
	corpus := docs.New(logger, gabyDB)

	for _, url := range args {
		if !strings.HasPrefix(url, "https://go.dev/") {
			log.Println("ignoring unrecognized url:", url)
			continue
		}

		// TODO: do we need to delete crawl.Page entries too?

		for doc := range corpus.Docs(url) {
			hasVector := " "
			if _, ok := gabyVectorDB.Get(doc.ID); ok {
				hasVector = "*"
			}
			fmt.Printf("%v %v", hasVector, doc.ID)

			fmt.Printf(" delete (y/N)? ")
			var a string
			fmt.Scanln(&a)
			if answer := strings.ToLower(strings.TrimSpace(a)); answer == "y" || answer == "yes" {
				gabyVectorDB.Delete(doc.ID)
				corpus.Delete(doc.ID)
				if _, ok := gabyVectorDB.Get(doc.ID); ok {
					log.Fatalf("error - %v not removed from vector db", doc.ID)
				}
				fmt.Print(" ↪ deleted")
			} else {
				fmt.Print(" ↪ skipped")
			}
			fmt.Println()
		}
	}
}

func initGCP() (storage.DB, storage.VectorDB) {
	ctx := context.TODO()

	if flags.project == "" {
		projectID, err := metadata.ProjectIDWithContext(ctx)
		if err != nil {
			log.Fatalf("metadata project ID: %v", err)
		}
		if projectID == "" {
			log.Fatal("project ID from metadata is empty")
		}
		flags.project = projectID
	}

	spec := &dbspec.Spec{
		Kind:     "firestore",
		Location: flags.project,
		Name:     flags.firestoredb,
	}
	db, err := spec.Open(context.TODO(), logger)
	if err != nil {
		log.Fatal(err)
	}

	const vectorDBNamespace = "gaby"
	vdb, err := firestore.NewVectorDB(ctx, slog.Default(), flags.project, flags.firestoredb, vectorDBNamespace)
	if err != nil {
		log.Fatal(err)
	}
	return db, vdb
}

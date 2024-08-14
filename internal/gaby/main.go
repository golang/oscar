// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"cloud.google.com/go/compute/metadata"
	"golang.org/x/oscar/internal/commentfix"
	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/embeddocs"
	"golang.org/x/oscar/internal/gcp/firestore"
	"golang.org/x/oscar/internal/gcp/gcphandler"
	"golang.org/x/oscar/internal/gcp/gcpsecret"
	"golang.org/x/oscar/internal/gemini"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/githubdocs"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/pebble"
	"golang.org/x/oscar/internal/related"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
)

var (
	searchMode    = flag.Bool("search", false, "run in interactive search mode (local mode only)")
	firestoreDB   = flag.String("firestoredb", "", "name of the Firestore DB to use (cloud mode only)")
	enableSync    = flag.Bool("enablesync", false, "sync the DB with GitHub and other external sources")
	enableChanges = flag.Bool("enablechanges", false, "allow changes to GitHub")
)

func main() {
	flag.Parse()
	ctx := context.Background()
	if onCloudRun() {
		runOnCloudRun(ctx)
	} else {
		runLocally(ctx)
	}
}

func runLocally(ctx context.Context) {
	lg := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	sdb := secret.Netrc()

	db, err := pebble.Open(lg, "gaby.db")
	if err != nil {
		log.Fatal(err)
	}

	vdb := storage.MemVectorDB(db, lg, "")

	gh := github.New(lg, db, secret.Netrc(), http.DefaultClient)
	// Ran during setup: gh.Add("golang/go")

	dc := docs.New(db)
	ai, err := gemini.NewClient(ctx, lg, sdb, http.DefaultClient, "text-embedding-004")
	if err != nil {
		log.Fatal(err)
	}

	if *searchMode {
		// Search loop.
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			log.Fatal(err)
		}
		vecs, err := ai.EmbedDocs(context.Background(), []llm.EmbedDoc{{Title: "", Text: string(data)}})
		if err != nil {
			log.Fatal(err)
		}
		vec := vecs[0]
		for _, r := range vdb.Search(vec, 20) {
			title := "?"
			if d, ok := dc.Get(r.ID); ok {
				title = d.Title
			}
			fmt.Printf(" %.5f %s # %s\n", r.Score, r.ID, title)
		}
		return
	}

	sync(ctx, lg, vdb, ai, dc, gh)

	cf := newGerritlinksCommentFixer(lg, gh)
	rp := newRelatedPoster(lg, db, gh, vdb, dc)

	for {
		sync(ctx, lg, vdb, ai, dc, gh)
		run(ctx, cf, rp)
		time.Sleep(2 * time.Minute)
	}
}

func runOnCloudRun(ctx context.Context) {
	var logLevel slog.LevelVar // defaults to Info
	lg := slog.New(gcphandler.New(&logLevel))

	projectID, err := metadata.ProjectIDWithContext(ctx)
	if err != nil {
		log.Fatalf("metadata project ID: %v", err)
	}
	if projectID == "" {
		log.Fatal("project ID from metadata is empty")
	}

	if *firestoreDB == "" {
		log.Fatal("missing -firestoredb flag")
	}

	slog.Info("Oscar starting",
		"syncEnabled", *enableSync,
		"changesEnabled", *enableChanges,
		"project", projectID,
		"FirestoreDB", *firestoreDB,
		"Cloud Run service", os.Getenv("K_SERVICE"))

	db, err := firestore.NewDB(ctx, lg, projectID, *firestoreDB)
	if err != nil {
		log.Fatal(err)
	}

	vdb, err := firestore.NewVectorDB(ctx, lg, projectID, *firestoreDB, "gaby")
	if err != nil {
		log.Fatal(err)
	}

	sdb, err := gcpsecret.NewSecretDB(ctx, projectID)
	if err != nil {
		log.Fatal(err)
	}

	gh := github.New(lg, db, sdb, http.DefaultClient)
	// TODO: if changes are not enabled, call EnableTesting
	// and save the the diverted edits somewhere.
	// TODO: for new databases, gh.Add("golang/go")

	dc := docs.New(db)
	ai, err := gemini.NewClient(ctx, lg, sdb, http.DefaultClient, "text-embedding-004")
	if err != nil {
		log.Fatal(err)
	}

	cf := newGerritlinksCommentFixer(lg, gh)
	rp := newRelatedPoster(lg, db, gh, vdb, dc)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Oscar\n")
		fmt.Fprintf(w, "project %s, firestore DB %s\n", projectID, *firestoreDB)
		fmt.Fprintf(w, "sync enabled: %t, changes enabled: %t\n", *enableSync, *enableChanges)
		fmt.Fprintf(w, "log level: %s\n", logLevel.Level())
	})
	// syncAndRun is called periodically by a Cloud Scheduler job.
	http.HandleFunc("/syncAndRun", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		sync(ctx, lg, vdb, ai, dc, gh)
		if *enableChanges {
			lg.Info("running")
			run(ctx, cf, rp)
		}
	})

	log.Fatal(http.ListenAndServe(":"+os.Getenv("PORT"), nil))
}

// Sync synchronizes the DBs with the current GitHub state.
func sync(ctx context.Context, lg *slog.Logger, vdb storage.VectorDB, ai *gemini.Client, dc *docs.Corpus, gh *github.Client) {
	if *enableSync {
		lg.Info("syncing")
		gh.Sync(ctx)
		githubdocs.Sync(ctx, lg, dc, gh)
		embeddocs.Sync(ctx, lg, vdb, ai, dc)
	}
}

type runnable interface {
	Run(context.Context)
}

// run calls Run once for each runnable.
func run(ctx context.Context, rs ...runnable) {
	for _, r := range rs {
		r.Run(ctx)
	}
}

func newGerritlinksCommentFixer(lg *slog.Logger, gh *github.Client) *commentfix.Fixer {
	cf := commentfix.New(lg, gh, "gerritlinks")
	cf.EnableProject("golang/go")
	cf.AutoLink(`\bCL ([0-9]+)\b`, "https://go.dev/cl/$1")
	cf.ReplaceURL(`\Qhttps://go-review.git.corp.google.com/\E`, "https://go-review.googlesource.com/")
	if *enableChanges {
		cf.EnableEdits()
	}
	return cf
}

func newRelatedPoster(lg *slog.Logger, db storage.DB, gh *github.Client, vdb storage.VectorDB, dc *docs.Corpus) *related.Poster {
	rp := related.New(lg, db, gh, vdb, dc, "related")
	rp.EnableProject("golang/go")
	rp.SkipBodyContains("— [watchflakes](https://go.dev/wiki/Watchflakes)")
	rp.SkipTitlePrefix("x/tools/gopls: release version v")
	rp.SkipTitleSuffix(" backport]")
	if *enableChanges {
		rp.EnablePosts()
	}
	return rp
}

// onCloudRun reports whether the current process is running on Cloud Run.
func onCloudRun() bool {
	// There is no definitive test, so look for some environment variables specified in
	// https://cloud.google.com/run/docs/container-contract#services-env-vars.
	return os.Getenv("K_SERVICE") != "" && os.Getenv("K_REVISION") != ""
}

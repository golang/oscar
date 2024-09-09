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
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/compute/metadata"
	ometric "go.opentelemetry.io/otel/metric"
	"golang.org/x/oscar/internal/commentfix"
	"golang.org/x/oscar/internal/crawl"
	"golang.org/x/oscar/internal/crawldocs"
	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/embeddocs"
	"golang.org/x/oscar/internal/gcp/firestore"
	"golang.org/x/oscar/internal/gcp/gcphandler"
	"golang.org/x/oscar/internal/gcp/gcpmetrics"
	"golang.org/x/oscar/internal/gcp/gcpsecret"
	"golang.org/x/oscar/internal/gcp/gemini"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/githubdocs"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/pebble"
	"golang.org/x/oscar/internal/related"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
)

var flags struct {
	search        bool
	project       string
	firestoredb   string
	enablesync    bool
	enablechanges bool
}

func init() {
	flag.BoolVar(&flags.search, "search", false, "run in interactive search mode")
	flag.StringVar(&flags.project, "project", "", "name of the Google Cloud Project")
	flag.StringVar(&flags.firestoredb, "firestoredb", "", "name of the Firestore DB to use")
	flag.BoolVar(&flags.enablesync, "enablesync", false, "sync the DB with GitHub and other external sources")
	flag.BoolVar(&flags.enablechanges, "enablechanges", false, "allow changes to GitHub")
}

// Gaby holds the state for gaby's execution.
type Gaby struct {
	ctx           context.Context
	cloud         bool                    // running on Cloud Run
	meta          map[string]string       // any metadata we want to expose
	addr          string                  // address to serve HTTP on
	githubProject string                  // github project to monitor and update
	actions       []func(context.Context) // functions to run every minute or so, or when triggered by a GitHub event

	slog      *slog.Logger     // slog output to use
	slogLevel *slog.LevelVar   // slog level, for changing as needed
	http      *http.Client     // http client to use
	db        storage.DB       // database to use
	vector    storage.VectorDB // vector database to use
	secret    secret.DB        // secret database to use
	docs      *docs.Corpus     // document corpus to use
	embed     llm.Embedder     // LLM embedder to use
	github    *github.Client   // github client to use
	meter     ometric.Meter    // used to create Open Telemetry instruments
}

func main() {
	flag.Parse()

	level := new(slog.LevelVar)
	g := &Gaby{
		ctx:           context.Background(),
		cloud:         onCloudRun(),
		meta:          map[string]string{},
		slog:          slog.New(gcphandler.New(level)),
		slogLevel:     level,
		http:          http.DefaultClient,
		addr:          "localhost:4229", // 4229 = gaby on a phone
		githubProject: "golang/go",
	}

	shutdown := g.initGCP()
	defer shutdown()

	var syncs, changes []func(context.Context)
	// Named functions to retrieve latest Watcher times.
	watcherLatests := map[string]func() timed.DBTime{}

	g.github = github.New(g.slog, g.db, g.secret, g.http)
	syncs = append(syncs, g.github.Sync)

	g.docs = docs.New(g.db)
	syncs = append(syncs, func(ctx context.Context) { githubdocs.Sync(ctx, g.slog, g.docs, g.github) })
	watcherLatests["githubdocs"] = func() timed.DBTime { return githubdocs.Latest(g.github) }

	ai, err := gemini.NewClient(g.ctx, g.slog, g.secret, g.http, "text-embedding-004")
	if err != nil {
		log.Fatal(err)
	}
	g.embed = ai
	syncs = append(syncs, func(ctx context.Context) { embeddocs.Sync(ctx, g.slog, g.vector, g.embed, g.docs) })
	watcherLatests["embeddocs"] = func() timed.DBTime { return embeddocs.Latest(g.docs) }

	cr := crawl.New(g.slog, g.db, g.http)
	cr.Add("https://go.dev/")
	cr.Allow(godevAllow...)
	cr.Deny(godevDeny...)
	cr.Clean(godevClean)
	// TODO(rsc): Crawling is too slow with a remote Firestore database.
	// Perhaps it will be okay when running on GCP.
	// For now, disable.
	if false {
		syncs = append(syncs, cr.Run)
		syncs = append(syncs, func(ctx context.Context) { crawldocs.Sync(ctx, g.slog, g.docs, cr) })
		watcherLatests["crawldocs"] = func() timed.DBTime { return crawldocs.Latest(cr) }
	}

	if flags.search {
		g.searchLoop()
		return
	}

	cf := commentfix.New(g.slog, g.github, "gerritlinks")
	cf.EnableProject(g.githubProject)
	cf.AutoLink(`\bCL ([0-9]+)\b`, "https://go.dev/cl/$1")
	cf.ReplaceURL(`\Qhttps://go-review.git.corp.google.com/\E`, "https://go-review.googlesource.com/")
	cf.EnableEdits()
	changes = append(changes, cf.Run)
	watcherLatests["gerritlinks fix"] = cf.Latest

	rp := related.New(g.slog, g.db, g.github, g.vector, g.docs, "related")
	rp.EnableProject(g.githubProject)
	rp.SkipBodyContains("— [watchflakes](https://go.dev/wiki/Watchflakes)")
	rp.SkipTitlePrefix("x/tools/gopls: release version v")
	rp.SkipTitleSuffix(" backport]")
	rp.EnablePosts()
	changes = append(changes, rp.Run)
	watcherLatests["related"] = rp.Latest

	if flags.enablesync {
		g.actions = append(g.actions, syncs...)
	}
	if flags.enablechanges {
		g.actions = append(g.actions, changes...)
	}

	// Install a metric that observes the latest values of the watchers each time metrics are sampled.
	g.registerWatcherMetric(watcherLatests)

	g.serveHTTP()
	log.Printf("serving %s", g.addr)

	if !g.cloud {
		// Simulate Cloud Scheduler.
		go g.localCron()
	}
	select {}
}

// initLocal initializes a local Gaby instance.
// No longer used, but here for experimentation.
func (g *Gaby) initLocal() {
	g.slog.Info("gaby local init", "flags", flags)

	g.secret = secret.Netrc()

	db, err := pebble.Open(g.slog, "gaby.db")
	if err != nil {
		log.Fatal(err)
	}
	g.db = db
	g.vector = storage.MemVectorDB(db, g.slog, "")
}

// initGCP initializes a Gaby instance to use GCP databases and other resources.
func (g *Gaby) initGCP() (shutdown func()) {
	if flags.project == "" {
		projectID, err := metadata.ProjectIDWithContext(g.ctx)
		if err != nil {
			log.Fatalf("metadata project ID: %v", err)
		}
		if projectID == "" {
			log.Fatal("project ID from metadata is empty")
		}
		flags.project = projectID
	}

	if g.cloud {
		port := os.Getenv("PORT")
		if port == "" {
			log.Fatal("$PORT not set")
		}
		g.meta["port"] = port
		g.addr = ":" + port
	}

	g.slog.Info("gaby cloud init",
		"flags", fmt.Sprintf("%+v", flags),
		"k_service", os.Getenv("K_SERVICE"),
		"k_revision", os.Getenv("K_REVISION"))

	if flags.firestoredb == "" {
		log.Fatal("missing -firestoredb flag")
	}

	db, err := firestore.NewDB(g.ctx, g.slog, flags.project, flags.firestoredb)
	if err != nil {
		log.Fatal(err)
	}
	g.db = db

	vdb, err := firestore.NewVectorDB(g.ctx, g.slog, flags.project, flags.firestoredb, "gaby")
	if err != nil {
		log.Fatal(err)
	}
	g.vector = vdb

	sdb, err := gcpsecret.NewSecretDB(g.ctx, flags.project)
	if err != nil {
		log.Fatal(err)
	}
	g.secret = sdb

	// Initialize metric collection.
	mp, err := gcpmetrics.NewMeterProvider(g.ctx, g.slog, flags.project)
	if err != nil {
		log.Fatal(err)
	}
	g.meter = mp.Meter("gcp")
	return func() {
		if err := mp.Shutdown(g.ctx); err != nil {
			log.Fatal(err)
		}
	}
}

// searchLoop runs an interactive search loop.
// It is called when the -search flag is specified.
func (g *Gaby) searchLoop() {
	// Search loop.
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		log.Fatal(err)
	}
	vecs, err := g.embed.EmbedDocs(context.Background(), []llm.EmbedDoc{{Title: "", Text: string(data)}})
	if err != nil {
		log.Fatal(err)
	}
	vec := vecs[0]
	for _, r := range g.vector.Search(vec, 20) {
		title := "?"
		if d, ok := g.docs.Get(r.ID); ok {
			title = d.Title
		}
		fmt.Printf(" %.5f %s # %s\n", r.Score, r.ID, title)
	}
}

// serveHTTP serves HTTP endpoints for Gaby.
func (g *Gaby) serveHTTP() {
	const (
		cronEndpoint        = "cron"
		setLevelEndpoint    = "setlevel"
		githubEventEndpoint = "github-event"
	)
	cronEndpointCounter := g.newEndpointCounter(cronEndpoint)
	githubEventEndpointCounter := g.newEndpointCounter(githubEventEndpoint)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Gaby\n")
		fmt.Fprintf(w, "meta: %+v\n", g.meta)
		fmt.Fprintf(w, "flags: %+v\n", flags)
		fmt.Fprintf(w, "log level: %v\n", g.slogLevel.Level())
	})

	// setlevel changes the log level dynamically.
	// Usage: /setlevel?l=LEVEL
	mux.HandleFunc("GET /"+setLevelEndpoint, func(w http.ResponseWriter, r *http.Request) {
		if err := g.slogLevel.UnmarshalText([]byte(r.FormValue("l"))); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Don't use "level" as a key: it will be misinterpreted as the severity of the log entry.
		g.slog.Info("log level set", "new-level", g.slogLevel.Level())
	})

	// cronEndpoint is called periodically by a Cloud Scheduler job.
	mux.HandleFunc("GET /"+cronEndpoint, func(w http.ResponseWriter, r *http.Request) {
		g.slog.Info(cronEndpoint + " start")
		defer g.slog.Info(cronEndpoint + " end")

		const cronLock = "gabycron"
		g.db.Lock(cronLock)
		defer g.db.Unlock(cronLock)

		for _, action := range g.actions {
			action(g.ctx)
		}
		cronEndpointCounter.Add(r.Context(), 1)
	})

	// githubEventEndpoint is called by a GitHub webhook when a new
	// event occurs on the githubProject repo.
	mux.HandleFunc("POST /"+githubEventEndpoint, func(w http.ResponseWriter, r *http.Request) {
		g.slog.Info(githubEventEndpoint + " start")
		defer g.slog.Info(githubEventEndpoint + " end")

		const githubEventLock = "gabygithubevent"
		g.db.Lock(githubEventLock)
		defer g.db.Unlock(githubEventLock)

		if err := g.handleGitHubEvent(r); err != nil {
			slog.Warn(githubEventEndpoint, "err", err)
		}

		githubEventEndpointCounter.Add(r.Context(), 1)
	})

	// /search: display a form for vector similarity search.
	// /search?q=...: perform a search using the value of q as input.
	mux.HandleFunc("GET /search", g.handleSearch)
	// Listen in this goroutine so that we can return a synchronous error
	// if the port is already in use or the address is otherwise invalid.
	// Run the actual server in a background goroutine.
	l, err := net.Listen("tcp", g.addr)
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		log.Fatal(http.Serve(l, mux))
	}()
}

// localCron simulates Cloud Scheduler by fetching our server's /cron endpoint once per minute.
func (g *Gaby) localCron() {
	for ; ; time.Sleep(1 * time.Minute) {
		resp, err := http.Get("http://" + g.addr + "/cron")
		if err != nil {
			g.slog.Error("localcron get", "err", err)
			continue
		}
		data, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			g.slog.Error("localcron get status", "status", resp.Status, "body", string(data))
		}
	}
}

// onCloudRun reports whether the current process is running on Cloud Run.
func onCloudRun() bool {
	// There is no definitive test, so look for some environment variables specified in
	// https://cloud.google.com/run/docs/container-contract#services-env-vars.
	return os.Getenv("K_SERVICE") != "" && os.Getenv("K_REVISION") != ""
}

// Crawling parameters

var godevAllow = []string{
	"https://go.dev/",
}

var godevDeny = []string{
	"https://go.dev/api/",
	"https://go.dev/change/",
	"https://go.dev/cl/",
	"https://go.dev/design/",
	"https://go.dev/dl/",
	"https://go.dev/issue/",
	"https://go.dev/lib/",
	"https://go.dev/misc/",
	"https://go.dev/play",
	"https://go.dev/s/",
	"https://go.dev/src/",
	"https://go.dev/test/",
}

func godevClean(u *url.URL) error {
	if u.Host == "go.dev" {
		u.Fragment = ""
		u.RawQuery = ""
		u.ForceQuery = false
		if strings.HasPrefix(u.Path, "/pkg") || strings.HasPrefix(u.Path, "/cmd") {
			u.RawQuery = "m=old"
		}
	}
	return nil
}

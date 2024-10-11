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
	"cloud.google.com/go/errorreporting"
	ometric "go.opentelemetry.io/otel/metric"
	"golang.org/x/oscar/internal/actions"
	"golang.org/x/oscar/internal/commentfix"
	"golang.org/x/oscar/internal/crawl"
	"golang.org/x/oscar/internal/discussion"
	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/embeddocs"
	"golang.org/x/oscar/internal/gcp/firestore"
	"golang.org/x/oscar/internal/gcp/gcphandler"
	"golang.org/x/oscar/internal/gcp/gcpmetrics"
	"golang.org/x/oscar/internal/gcp/gcpsecret"
	"golang.org/x/oscar/internal/gcp/gemini"
	"golang.org/x/oscar/internal/gerrit"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/pebble"
	"golang.org/x/oscar/internal/related"
	"golang.org/x/oscar/internal/search"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/oscar/internal/storage/timed"
)

type gabyFlags struct {
	search        bool
	project       string
	firestoredb   string
	enablesync    bool
	enablechanges bool
	level         string
}

var flags gabyFlags

func init() {
	flag.BoolVar(&flags.search, "search", false, "run in interactive search mode")
	flag.StringVar(&flags.project, "project", "", "name of the Google Cloud Project")
	flag.StringVar(&flags.firestoredb, "firestoredb", "", "name of the Firestore DB to use")
	flag.BoolVar(&flags.enablesync, "enablesync", false, "sync the DB with GitHub and other external sources")
	flag.BoolVar(&flags.enablechanges, "enablechanges", false, "allow changes to GitHub")
	flag.StringVar(&flags.level, "level", "info", "initial log level")
}

// Gaby holds the state for gaby's execution.
type Gaby struct {
	ctx            context.Context
	cloud          bool              // running on Cloud Run
	meta           map[string]string // any metadata we want to expose
	addr           string            // address to serve HTTP on
	githubProject  string            // github project to monitor and update
	gerritProjects []string          // gerrit projects to monitor and update

	slog      *slog.Logger           // slog output to use
	slogLevel *slog.LevelVar         // slog level, for changing as needed
	http      *http.Client           // http client to use
	db        storage.DB             // database to use
	vector    storage.VectorDB       // vector database to use
	secret    secret.DB              // secret database to use
	docs      *docs.Corpus           // document corpus to use
	embed     llm.Embedder           // LLM embedder to use
	github    *github.Client         // github client to use
	disc      *discussion.Client     // github discussion client to use
	gerrit    *gerrit.Client         // gerrit client to use
	crawler   *crawl.Crawler         // web crawler to use
	meter     ometric.Meter          // used to create Open Telemetry instruments
	report    *errorreporting.Client // used to report important gaby errors to Cloud Error Reporting service

	relatedPoster *related.Poster   // used to post related issues
	commentFixer  *commentfix.Fixer // used to fix GitHub comments
}

func main() {
	flag.Parse()

	level := new(slog.LevelVar)
	if err := level.UnmarshalText([]byte(flags.level)); err != nil {
		log.Fatal(err)
	}
	g := &Gaby{
		ctx:            context.Background(),
		cloud:          onCloudRun(),
		meta:           map[string]string{},
		slog:           slog.New(gcphandler.New(level)),
		slogLevel:      level,
		http:           http.DefaultClient,
		addr:           "localhost:4229", // 4229 = gaby on a phone
		githubProject:  "golang/go",
		gerritProjects: []string{"go"},
	}

	shutdown := g.initGCP()
	defer shutdown()

	g.github = github.New(g.slog, g.db, g.secret, g.http)
	g.disc = discussion.New(g.ctx, g.slog, g.secret, g.db)
	_ = g.disc.Add(g.githubProject) // only needed once per g.db lifetime

	g.gerrit = gerrit.New("go-review.googlesource.com", g.slog, g.db, g.secret, g.http)
	for _, project := range g.gerritProjects {
		_ = g.gerrit.Add(project) // in principle needed only once per g.db lifetime
	}

	g.docs = docs.New(g.slog, g.db)

	ai, err := gemini.NewClient(g.ctx, g.slog, g.secret, g.http, "text-embedding-004")
	if err != nil {
		log.Fatal(err)
	}
	g.embed = ai

	cr := crawl.New(g.slog, g.db, g.http)
	cr.Add("https://go.dev/")
	cr.Allow(godevAllow...)
	cr.Deny(godevDeny...)
	cr.Clean(godevClean)
	g.crawler = cr

	if flags.search {
		g.searchLoop()
		return
	}

	cf := commentfix.New(g.slog, g.github, g.db, "gerritlinks")
	cf.EnableProject(g.githubProject)
	cf.AutoLink(`\bCL ([0-9]+)\b`, "https://go.dev/cl/$1")
	cf.ReplaceURL(`\Qhttps://go-review.git.corp.google.com/\E`, "https://go-review.googlesource.com/")
	cf.EnableEdits()
	g.commentFixer = cf

	rp := related.New(g.slog, g.db, g.github, g.vector, g.docs, "related")
	rp.EnableProject(g.githubProject)
	rp.SkipBodyContains("— [watchflakes](https://go.dev/wiki/Watchflakes)")
	rp.SkipTitlePrefix("x/tools/gopls: release version v")
	rp.SkipTitleSuffix(" backport]")
	rp.EnablePosts()
	g.relatedPoster = rp

	// Named functions to retrieve latest Watcher times.
	watcherLatests := map[string]func() timed.DBTime{
		github.DocWatcherID:     docs.LatestFunc(g.github),
		gerrit.DocWatcherID:     docs.LatestFunc(g.gerrit),
		discussion.DocWatcherID: docs.LatestFunc(g.disc),
		crawl.DocWatcherID:      docs.LatestFunc(cr),

		"embeddocs": func() timed.DBTime { return embeddocs.Latest(g.docs) },

		"gerritlinks fix": cf.Latest,
		"related":         rp.Latest,
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

	// Initialize error reporting.
	rep, err := errorreporting.NewClient(g.ctx, flags.project, errorreporting.Config{
		ServiceName: os.Getenv("K_SERVICE"),
		OnError: func(err error) {
			g.slog.Error("error reporting", "err", err)
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	g.report = rep

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

	rs, err := search.Query(context.Background(), g.vector, g.docs, g.embed, &search.QueryRequest{
		Options: search.Options{
			Limit: 20,
		},
		EmbedDoc: llm.EmbedDoc{Text: string(data)},
	})
	if err != nil {
		log.Fatal(err)
	}

	for _, r := range rs {
		fmt.Printf(" %.5f %s # %s\n", r.Score, r.ID, r.Title)
	}
}

// serveHTTP serves HTTP endpoints for Gaby.
func (g *Gaby) serveHTTP() {
	report := func(err error) {
		g.slog.Error("reporting", "err", err)
		g.report.Report(errorreporting.Entry{Error: err})
	}
	mux := g.newServer(report)
	// Listen in this goroutine so that we can return a synchronous error
	// if the port is already in use or the address is otherwise invalid.
	// Run the actual server in a background goroutine.
	l, err := net.Listen("tcp", g.addr)
	if err != nil {
		report(err)
		log.Fatal(err)
	}
	go func() {
		if err := http.Serve(l, mux); err != nil {
			report(err)
			log.Fatal(err)
		}
	}()
}

// newServer creates a new [http.ServeMux] that uses report to
// process server creation and endpoint errors.
func (g *Gaby) newServer(report func(error)) *http.ServeMux {
	const (
		cronEndpoint        = "cron"
		syncEndpoint        = "sync"
		setLevelEndpoint    = "setlevel"
		githubEventEndpoint = "github-event"
		crawlEndpoint       = "crawl"
	)
	cronEndpointCounter := g.newEndpointCounter(cronEndpoint)
	crawlEndpointCounter := g.newEndpointCounter(crawlEndpoint)
	githubEventEndpointCounter := g.newEndpointCounter(githubEventEndpoint)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Gaby\n")
		fmt.Fprintf(w, "meta: %+v\n", g.meta)
		fmt.Fprintf(w, "flags: %+v\n", flags)
		fmt.Fprintf(w, "log level: %v\n", g.slogLevel.Level())
	})

	// serve static files
	mux.Handle("GET /static/", http.FileServerFS(staticFS))

	// setlevel changes the log level dynamically.
	// Usage: /setlevel?l=LEVEL
	mux.HandleFunc("GET /"+setLevelEndpoint, func(w http.ResponseWriter, r *http.Request) {
		if err := g.slogLevel.UnmarshalText([]byte(r.FormValue("l"))); err != nil {
			report(err)
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

		if errs := g.syncAndRunAll(g.ctx); len(errs) != 0 {
			for _, err := range errs {
				report(err)
			}
		}
		cronEndpointCounter.Add(r.Context(), 1)
	})

	// crawlEndpoint triggers the web crawl configured in [Gaby.crawler].
	// It is intended to be triggered by a Cloud Scheduler job (or similar)
	// to run periodically.
	mux.HandleFunc("GET /"+crawlEndpoint, func(w http.ResponseWriter, r *http.Request) {
		g.slog.Info(crawlEndpoint + " start")
		defer g.slog.Info(crawlEndpoint + " end")

		const lock = "gabycrawl"
		g.db.Lock(lock)
		defer g.db.Unlock(lock)

		if err := g.crawl(r.Context()); err != nil {
			report(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}

		crawlEndpointCounter.Add(r.Context(), 1)
	})

	// githubEventEndpoint is called by a GitHub webhook when a new
	// event occurs on the githubProject repo.
	mux.HandleFunc("POST /"+githubEventEndpoint, func(w http.ResponseWriter, r *http.Request) {
		g.slog.Info(githubEventEndpoint + " start")
		defer g.slog.Info(githubEventEndpoint + " end")

		const githubEventLock = "gabygithubevent"
		g.db.Lock(githubEventLock)
		defer g.db.Unlock(githubEventLock)

		if handled, err := g.handleGitHubEvent(r, &flags); err != nil {
			report(err)
			slog.Warn(githubEventEndpoint, "err", err)
		} else if handled {
			slog.Info(githubEventEndpoint + " success")
		} else {
			slog.Debug(githubEventEndpoint + " skipped event")
		}

		githubEventEndpointCounter.Add(r.Context(), 1)
	})

	// syncEndpoint is called manually to invoke a specific sync job.
	// It performs a sync if enablesync is true.
	// Usage: /sync?job={github | crawl | gerrit | discussion}
	mux.HandleFunc("GET /"+syncEndpoint, func(w http.ResponseWriter, r *http.Request) {
		g.slog.Info(syncEndpoint + " start")
		defer g.slog.Info(syncEndpoint + " end")

		if !flags.enablesync {
			fmt.Fprint(w, "exiting: sync is not enabled")
			return
		}

		const syncLock = "gabysync"
		g.db.Lock(syncLock)
		defer g.db.Unlock(syncLock)

		job := r.FormValue("job")
		var err error
		switch job {
		case "github":
			err = g.syncGitHubIssues(g.ctx)
		case "discussion":
			err = g.syncGitHubDiscussions(g.ctx)
		case "crawl":
			err = g.syncCrawl(g.ctx)
		case "gerrit":
			err = g.syncGerrit(g.ctx)
		default:
			err = fmt.Errorf("unrecognized sync job %s", job)
		}

		if err == nil { // embed only if sync succeeded
			err = g.embedAll(g.ctx)
		}

		if err != nil {
			http.Error(w, fmt.Sprintf("sync: error %v for %s", err, job), http.StatusInternalServerError)
			g.slog.Error("sync", "job", job, "error", err)
		}
	})

	// /search: display a form for vector similarity search.
	// /search?q=...: perform a search using the value of q as input.
	mux.HandleFunc("GET /search", g.handleSearch)

	// /api/search: perform a vector similarity search.
	// POST because the arguments to the request are in the body.
	mux.HandleFunc("POST /api/search", g.handleSearchAPI)

	// /actionlog: display action log
	mux.HandleFunc("GET /actionlog", g.handleActionLog)

	// /reviews: display review dashboard
	mux.HandleFunc("GET /reviews", g.handleReviewDashboard)

	return mux
}

// crawl crawls the webpages configured in [Gaby.crawler], adds them
// to the documents corpus [Gaby.docs], and stores their embeddings
// in the vector database [Gaby.vector].
// if flags.enablesync is false, it is a no-op.
func (g *Gaby) crawl(ctx context.Context) error {
	if !flags.enablesync {
		return nil
	}
	if err := g.syncCrawl(ctx); err != nil {
		return err
	}
	return g.embedAll(ctx)
}

// syncCrawl crawls webpages and adds them to the document corpus.
func (g *Gaby) syncCrawl(ctx context.Context) error {
	g.db.Lock(gabyCrawlLock)
	defer g.db.Unlock(gabyCrawlLock)

	if err := g.crawler.Run(ctx); err != nil {
		return err
	}
	docs.Sync(g.docs, g.crawler)
	return nil
}

// syncAndRunAll runs all fast syncs (if enablesync is true) and Gaby actions
// (if enablechanges is true).
// It does not perform slow syncs, such as crawling, which must be triggered
// separately.
// It treats all errors as non-fatal, returning a slice of all errors
// that occurred.
func (g *Gaby) syncAndRunAll(ctx context.Context) (errs []error) {
	check := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}

	if flags.enablesync {
		// Independent syncs can run in any order.
		check(g.syncGitHubIssues(ctx))
		check(g.syncGitHubDiscussions(ctx))
		check(g.syncGerrit(ctx))

		// Embed must happen last.
		check(g.embedAll(ctx))
	}

	if flags.enablechanges {
		// Changes can run in any order.
		// Write all changes to the action log.
		check(g.fixAllComments(ctx))
		check(g.postAllRelated(ctx))
		// Apply all actions.
		actions.Run(ctx, g.slog, g.db)
	}

	return errs
}

const (
	gabyGitHubSyncLock     = "gabygithubsync"
	gabyDiscussionSyncLock = "gabydiscussionsync"
	gabyGerritSyncLock     = "gabygerritsync"
	gabyEmbedLock          = "gabyembedsync"
	gabyCrawlLock          = "gabycrawlsync"

	gabyFixCommentLock  = "gabyfixcommentaction"
	gabyPostRelatedLock = "gabyrelatedaction"
)

func (g *Gaby) syncGitHubIssues(ctx context.Context) error {
	g.db.Lock(gabyGitHubSyncLock)
	defer g.db.Unlock(gabyGitHubSyncLock)

	// Download new issue events from all GitHub projects.
	if err := g.github.Sync(ctx); err != nil {
		return err
	}
	// Store newly downloaded GitHub issue events in the document
	// database.
	docs.Sync(g.docs, g.github)
	return nil
}

func (g *Gaby) syncGitHubDiscussions(ctx context.Context) error {
	g.db.Lock(gabyDiscussionSyncLock)
	defer g.db.Unlock(gabyDiscussionSyncLock)

	// Download new discussions and discussion comments from
	// all GitHub projects.
	if err := g.disc.Sync(ctx); err != nil {
		return err
	}

	// Store newly downloaded GitHub discussions in the document database.
	docs.Sync(g.docs, g.disc)
	return nil
}

func (g *Gaby) syncGerrit(ctx context.Context) error {
	g.db.Lock(gabyGerritSyncLock)
	defer g.db.Unlock(gabyGerritSyncLock)

	// Download new events from all gerrit projects.
	if err := g.gerrit.Sync(ctx); err != nil {
		return err
	}
	// Store newly downloaded gerrit events in the document database.
	docs.Sync(g.docs, g.gerrit)
	return nil
}

// embedAll store embeddings for all new documents in the vector database.
// This must happen after all other syncs.
func (g *Gaby) embedAll(ctx context.Context) error {
	g.db.Lock(gabyEmbedLock)
	defer g.db.Unlock(gabyEmbedLock)

	return embeddocs.Sync(ctx, g.slog, g.vector, g.embed, g.docs)
}

func (g *Gaby) fixAllComments(ctx context.Context) error {
	g.db.Lock(gabyFixCommentLock)
	defer g.db.Unlock(gabyFixCommentLock)

	return g.commentFixer.Run(ctx)
}

func (g *Gaby) postAllRelated(ctx context.Context) error {
	g.db.Lock(gabyPostRelatedLock)
	defer g.db.Unlock(gabyPostRelatedLock)

	return g.relatedPoster.Run(ctx)
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

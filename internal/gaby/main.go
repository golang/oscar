// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"cloud.google.com/go/compute/metadata"
	"cloud.google.com/go/errorreporting"
	ometric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"golang.org/x/oscar/internal/actions"
	"golang.org/x/oscar/internal/bisect"
	"golang.org/x/oscar/internal/commentfix"
	"golang.org/x/oscar/internal/crawl"
	"golang.org/x/oscar/internal/dbspec"
	"golang.org/x/oscar/internal/discussion"
	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/embeddocs"
	"golang.org/x/oscar/internal/gcp/checks"
	"golang.org/x/oscar/internal/gcp/firestore"
	"golang.org/x/oscar/internal/gcp/gcphandler"
	"golang.org/x/oscar/internal/gcp/gcpmetrics"
	"golang.org/x/oscar/internal/gcp/gcpsecret"
	"golang.org/x/oscar/internal/gcp/gemini"
	"golang.org/x/oscar/internal/gcp/tasks"
	"golang.org/x/oscar/internal/gerrit"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/googlegroups"
	"golang.org/x/oscar/internal/labels"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/llmapp"
	"golang.org/x/oscar/internal/overview"
	"golang.org/x/oscar/internal/pebble"
	"golang.org/x/oscar/internal/queue"
	"golang.org/x/oscar/internal/related"
	"golang.org/x/oscar/internal/rules"
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
	testactions   bool
	level         string
	overlay       string
	autoApprove   string // list of packages that do not require manual approval
	enforcePolicy bool
}

var flags gabyFlags

func init() {
	flag.BoolVar(&flags.search, "search", false, "run in interactive search mode")
	flag.StringVar(&flags.project, "project", "", "name of the Google Cloud Project")
	flag.StringVar(&flags.firestoredb, "firestoredb", "", "name of the Firestore DB to use")
	flag.BoolVar(&flags.enablesync, "enablesync", false, "sync the DB with GitHub and other external sources")
	flag.BoolVar(&flags.enablechanges, "enablechanges", false, "allow changes to GitHub")
	flag.BoolVar(&flags.testactions, "testactions", false, "allow approved actions to run (for testing only)")
	flag.StringVar(&flags.level, "level", "info", "initial log level")
	flag.StringVar(&flags.overlay, "overlay", "", "spec for overlay to DB; see internal/dbspec for syntax")
	flag.StringVar(&flags.autoApprove, "autoapprove", "", "comma-separated list of packages whose actions do not require approval")
	flag.BoolVar(&flags.enforcePolicy, "enforcepolicy", false, "whether to enforce safety policies on LLM inputs and outputs")
}

// Gaby holds the state for gaby's execution.
type Gaby struct {
	ctx            context.Context
	cloud          bool              // running on Cloud Run
	meta           map[string]string // any metadata we want to expose
	addr           string            // address to serve HTTP on
	githubProjects []string          // github projects to monitor and update
	gerritProjects []string          // gerrit projects to monitor and update
	googleGroups   []string          // google groups to monitor and update

	slog      *slog.Logger           // slog output to use
	slogLevel *slog.LevelVar         // slog level, for changing as needed
	http      *http.Client           // http client to use
	db        storage.DB             // database to use
	vector    storage.VectorDB       // vector database to use
	secret    secret.DB              // secret database to use
	docs      *docs.Corpus           // document corpus to use
	embed     llm.Embedder           // LLM embedder to use
	llm       llm.ContentGenerator   // LLM content generator to use
	policy    llm.PolicyChecker      // LLM checker to use
	llmapp    *llmapp.Client         // LLM client to use
	github    *github.Client         // github client to use
	disc      *discussion.Client     // github discussion client to use
	gerrit    *gerrit.Client         // gerrit client to use
	ggroups   *googlegroups.Client   // google groups client to use
	crawler   *crawl.Crawler         // web crawler to use
	bisect    *bisect.Client         // bisect client to use
	meter     ometric.Meter          // used to create Open Telemetry instruments
	report    *errorreporting.Client // used to report important gaby errors to Cloud Error Reporting service

	relatedPoster *related.Poster   // used to post related issues
	rulesPoster   *rules.Poster     // used to post rule violations
	commentFixer  *commentfix.Fixer // used to fix GitHub comments
	overview      *overview.Client  // used to generate and post overviews
	labeler       *labels.Labeler   // used to assign labels to issues
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
		githubProjects: []string{"golang/go"},
		gerritProjects: []string{"go"},
		googleGroups:   []string{"golang-nuts"},
	}

	autoApprovePkgs, err := parseApprovalPkgs(flags.autoApprove)
	if err != nil {
		log.Fatal(err)
	}

	shutdown := g.initGCP() // sets up g.db, g.vector, g.secret, ...
	defer shutdown()

	g.github = github.New(g.slog, g.db, g.secret, g.http)
	for _, project := range g.githubProjects {
		if err := g.github.Add(project); err != nil {
			log.Fatalf("github.Add failed: %v", err)
		}
	}
	g.disc = discussion.New(g.ctx, g.slog, g.secret, g.db)
	for _, project := range g.githubProjects {
		if err := g.disc.Add(project); err != nil {
			log.Fatalf("discussion.Add failed: %v", err)
		}
	}

	g.gerrit = gerrit.New("go-review.googlesource.com", g.slog, g.db, g.secret, g.http)
	for _, project := range g.gerritProjects {
		if err := g.gerrit.Add(project); err != nil {
			log.Fatalf("gerrit.Add failed: %v", err)
		}
	}

	g.ggroups = googlegroups.New(g.slog, g.db, g.secret, g.http)
	for _, group := range g.googleGroups {
		if err := g.ggroups.Add(group); err != nil {
			log.Fatalf("googlegroups.Add failed: %v", err)
		}
	}

	g.docs = docs.New(g.slog, g.db)

	ai, err := gemini.NewClient(g.ctx, g.slog, g.secret, g.http, gemini.DefaultEmbeddingModel, gemini.DefaultGenerativeModel)
	if err != nil {
		log.Fatal(err)
	}
	g.embed = ai
	g.llm = ai
	g.llmapp = llmapp.NewWithChecker(g.slog, ai, g.policy, g.db)
	ov := overview.New(g.slog, g.db, g.github, g.llmapp, "overview", "gabyhelp")
	for _, proj := range g.githubProjects {
		ov.EnableProject(proj)
	}
	if !slices.Contains(autoApprovePkgs, "overview") {
		ov.RequireApproval()
	}
	g.overview = ov

	cr := crawl.New(g.slog, g.db, g.http)
	cr.Add("https://go.dev/")
	cr.Allow(godevAllow...)
	cr.Deny(godevDeny...)
	cr.Clean(godevClean)
	g.crawler = cr

	// Set up bisection if we are on Cloud Run.
	if g.cloud {
		q, err := taskQueue(g)
		if err != nil {
			log.Fatalf("task Queue creation failed: %v", err)
		}
		bs := bisect.New(g.slog, g.db, q)
		g.bisect = bs
	}

	if flags.search {
		g.searchLoop()
		return
	}

	cf := commentfix.New(g.slog, g.github, g.db, "gerritlinks")
	for _, proj := range g.githubProjects {
		cf.EnableProject(proj)
	}
	cf.AutoLink(`\bCL ([0-9]+)\b`, "https://go.dev/cl/$1")
	cf.ReplaceURL(`\Qhttps://go-review.git.corp.google.com/\E`, "https://go-review.googlesource.com/")
	cf.EnableEdits()
	if !slices.Contains(autoApprovePkgs, "commentfix") {
		cf.RequireApproval()
	}
	g.commentFixer = cf

	rp := related.New(g.slog, g.db, g.github, g.vector, g.docs, "related")
	for _, proj := range g.githubProjects {
		rp.EnableProject(proj)
	}
	// TODO(hyangah): shouldn't these rules be configured differently for different github projects?
	rp.SkipBodyContains("— [watchflakes](https://go.dev/wiki/Watchflakes)")
	rp.SkipTitlePrefix("x/tools/gopls: release version v")
	rp.SkipTitleSuffix(" backport]")
	rp.SkipTitlePrefix("security: fix CVE-") // CVE issues are boilerplate
	rp.EnablePosts()
	if !slices.Contains(autoApprovePkgs, "related") {
		rp.RequireApproval()
	}
	g.relatedPoster = rp

	rulep := rules.New(g.slog, g.db, g.github, g.llm, "rules")
	for _, proj := range g.githubProjects {
		rulep.EnableProject(proj)
	}
	rulep.EnablePosts()
	if !slices.Contains(autoApprovePkgs, "rules") {
		rulep.RequireApproval()
	}
	g.rulesPoster = rulep

	labeler := labels.New(g.slog, g.db, g.github, ai, "gabyhelp")
	for _, proj := range g.githubProjects {
		// TODO: support other projects.
		if proj != "golang/go" {
			continue
		}
		labeler.EnableProject(proj)
	}
	labeler.SkipAuthor("gopherbot")
	labeler.EnableLabels()
	if !slices.Contains(autoApprovePkgs, "labels") {
		labeler.RequireApproval()
	}
	g.labeler = labeler

	// Named functions to retrieve latest Watcher times.
	watcherLatests := map[string]func() timed.DBTime{
		github.DocWatcherID:       docs.LatestFunc(g.github),
		gerrit.DocWatcherID:       docs.LatestFunc(g.gerrit),
		discussion.DocWatcherID:   docs.LatestFunc(g.disc),
		crawl.DocWatcherID:        docs.LatestFunc(cr),
		googlegroups.DocWatcherID: docs.LatestFunc(g.ggroups),

		"embeddocs": func() timed.DBTime { return embeddocs.Latest(g.docs) },

		"gerritlinks fix": cf.Latest,
		"related":         rp.Latest,
		"rules":           rulep.Latest,
		"labeler":         labeler.Latest,
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

var validApprovalPkgs = []string{"commentfix", "related", "rules", "labels", "overview"}

// parseApprovalPkgs parses a comma-separated list of package names,
// checking that the packages are valid.
func parseApprovalPkgs(s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	pkgs := strings.Split(s, ",")
	for _, p := range pkgs {
		if !slices.Contains(validApprovalPkgs, p) {
			return nil, fmt.Errorf("invalid arg %q to -autoapprove: valid values are: %s",
				p, strings.Join(validApprovalPkgs, ", "))
		}
	}
	return pkgs, nil
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
	shutdown = func() {}

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
	spec := &dbspec.Spec{
		Kind:     "firestore",
		Location: flags.project,
		Name:     flags.firestoredb,
	}
	db, err := spec.Open(g.ctx, g.slog)
	if err != nil {
		log.Fatal(err)
	}
	g.db = db

	const vectorDBNamespace = "gaby"
	if flags.overlay != "" {
		spec, err := dbspec.Parse(flags.overlay)
		if err != nil {
			log.Fatal(err)
		}
		if spec.IsVector {
			log.Fatal("omit vector DB spec for -overlay")
		}
		odb, err := spec.Open(g.ctx, g.slog)
		if err != nil {
			log.Fatal(err)
		}
		g.db = storage.NewOverlayDB(odb, g.db)
		g.vector = storage.MemVectorDB(g.db, g.slog, vectorDBNamespace)
	} else {
		vdb, err := firestore.NewVectorDB(g.ctx, g.slog, spec.Location, spec.Name, vectorDBNamespace)
		if err != nil {
			log.Fatal(err)
		}
		g.vector = vdb
	}

	sdb, err := gcpsecret.NewSecretDB(g.ctx, flags.project)
	if err != nil {
		log.Fatal(err)
	}
	g.secret = sdb

	if flags.enforcePolicy {
		llmchecker, err := checks.New(g.ctx, g.slog, flags.project, llm.AllPolicyTypes())
		if err != nil {
			log.Fatal(err)
		}
		g.policy = llmchecker
	}

	// Initialize error reporting if we are running on Cloud Run.
	if g.cloud {
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
	}

	// Initialize metric collection if we are running on Cloud Run.
	if g.cloud {
		mp, err := gcpmetrics.NewMeterProvider(g.ctx, g.slog, flags.project)
		if err != nil {
			log.Fatal(err)
		}
		g.meter = mp.Meter("gcp")

		shutdown = func() {
			if err := mp.Shutdown(g.ctx); err != nil {
				log.Fatal(err)
			}
		}
	} else {
		g.meter = noop.Meter{}
	}
	return shutdown
}

// taskQueue returns a bisection Cloud Task queue.
func taskQueue(g *Gaby) (queue.Queue, error) {
	sa, err := metadata.Email("")
	if err != nil {
		return nil, err
	}

	const location = "us-central1"
	project := flags.project
	service := os.Getenv("K_SERVICE")

	mode := ""
	if strings.Contains(service, "prod") {
		mode = "prod"
	} else if strings.Contains(service, "devel") {
		mode = "devel"
	} else {
		return nil, fmt.Errorf("K_SERVICE in unexpected format: %s", service)
	}

	projID, err := metadata.NumericProjectIDWithContext(g.ctx)
	if err != nil {
		return nil, err
	}
	// We can create the service URL from mode, project number, and location.
	// The actual service URL might change, but this value will always be valid.
	qurl := fmt.Sprintf("https://gaby-%s-%s.%s.run.app", mode, projID, location)

	qm := &queue.Metadata{
		ServiceAccount: sa,
		Location:       location,
		Project:        project,
		// {prod|devel}-bisection-tasks are two
		// existing bisection Cloud Tasks.
		QueueName: mode + "-bisection-tasks",
		QueueURL:  qurl,
	}
	g.slog.Info("queue.Info meta", "data", fmt.Sprintf("%+v", qm))

	return tasks.New(g.ctx, qm)
}

// searchLoop runs an interactive search loop.
// It is called when the -search flag is specified.
func (g *Gaby) searchLoop() {
	fmt.Fprintln(os.Stderr, "# ctrl+d to start search.")
	fmt.Fprintln(os.Stderr, "# ctrl+c to exit.")
	fmt.Fprintln(os.Stderr, "")
	for {
		fmt.Fprintf(os.Stderr, "> ")
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			log.Fatal(err)
		}
		line := string(data)
		rs, err := g.search(context.Background(), line, search.Options{})
		if err != nil {
			log.Fatal(err)
		}

		for _, r := range rs {
			fmt.Printf(" %.5f %s # %s\n", r.Score, r.ID, r.Title)
		}
		fmt.Fprintf(os.Stderr, "\n")
	}
}

// serveHTTP serves HTTP endpoints for Gaby.
func (g *Gaby) serveHTTP() {
	report := func(err error, r *http.Request) {
		g.slog.Error("reporting", "err", err)
		if g.report != nil {
			g.report.Report(errorreporting.Entry{Error: err, Req: r})
		}
	}
	mux := g.newServer(report)
	// Listen in this goroutine so that we can return a synchronous error
	// if the port is already in use or the address is otherwise invalid.
	// Run the actual server in a background goroutine.
	l, err := net.Listen("tcp", g.addr)
	if err != nil {
		report(err, nil)
		log.Fatal(err)
	}
	go func() {
		if err := http.Serve(l, mux); err != nil {
			report(err, nil)
			log.Fatal(err)
		}
	}()
}

// newServer creates a new [http.ServeMux] that uses report to
// process server creation and endpoint errors.
func (g *Gaby) newServer(report func(error, *http.Request)) *http.ServeMux {
	const (
		cronEndpoint        = "cron"
		syncEndpoint        = "sync"
		setLevelEndpoint    = "setlevel"
		githubEventEndpoint = "github-event"
		crawlEndpoint       = "crawl"
		bisectEndpoint      = "bisect"
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
			report(err, r)
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
				report(err, r)
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
			report(err, r)
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
			report(err, r)
			slog.Warn(githubEventEndpoint, "err", err)
		} else if handled {
			slog.Info(githubEventEndpoint + " success")
		} else {
			slog.Debug(githubEventEndpoint + " skipped event")
		}

		githubEventEndpointCounter.Add(r.Context(), 1)
	})

	// bisectEndpoint executes bisection tasks.
	// TODO: separate bisection into a separate service.
	// That would allow us to better handle concurrency
	// and resource requirements.
	mux.HandleFunc("POST /"+bisectEndpoint, func(w http.ResponseWriter, r *http.Request) {
		g.slog.Info(bisectEndpoint + " start")
		defer g.slog.Info(bisectEndpoint + " end")

		// Do not respond a 4xx error code as that can
		// make Cloud Task repeat the bisection. Instead,
		// we use a dedicated 2xx code.
		const errorCode = 299

		tid := r.FormValue("id")
		if tid == "" {
			w.WriteHeader(errorCode)
			report(errors.New("bisection: no task id provided"), r)
			return
		}

		// c.bisect.Bisect below will lock on a specific
		// bisection task, so there is no need to do
		// locking here.

		if err := g.bisect.Bisect(g.ctx, tid); err != nil {
			w.WriteHeader(errorCode)
			report(err, r)
			g.slog.Info(bisectEndpoint+" failure", "err", err)
		}
	})

	// runactions runs all pending, approved actions in the action log.
	// Useful for immediately running actions that have just been approved by a human,
	// or for testing a new action in the devel environment.
	mux.HandleFunc("GET /runactions", func(w http.ResponseWriter, r *http.Request) {
		g.db.Lock(runActionsLock)
		defer g.db.Unlock(runActionsLock)

		if flags.enablechanges || flags.testactions {
			report := actions.RunWithReport(g.ctx, g.slog, g.db)
			_, _ = w.Write(storage.JSON(report))
		} else {
			http.Error(w, "runactions: flag -enablechanges or -testactions not set", http.StatusInternalServerError)
		}
	})

	// syncEndpoint is called manually to invoke a specific sync job.
	// It performs a sync if enablesync is true.
	// Usage: /sync?job={github | crawl | gerrit | discussion | groups}
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
		case "groups":
			err = g.syncGroups(g.ctx)
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

	// action-decision: approve or deny an action
	mux.HandleFunc("GET /action-decision", g.handleActionDecision)
	// action-rerun: rerun a failed action
	mux.HandleFunc("GET /action-rerun", g.handleActionRerun)

	get := func(p pageID) string {
		return "GET " + p.Endpoint()
	}

	// /search: display a form for vector similarity search.
	// /search?q=...: perform a search using the value of q as input.
	mux.HandleFunc(get(searchID), g.handleSearch)

	// /overview: display a form for LLM-generated overviews of data.
	// /overview?q=...: generate an overview using the value of q as input.
	mux.HandleFunc(get(overviewID), g.handleOverview)

	// /rules: display a form for entering an issue to check for rule violations.
	// /rules?q=...: generate a list of violated rules for issue q.
	mux.HandleFunc(get(rulesID), g.handleRules)

	// /labels: display label classifications for issues.
	// /labels?q=...: report on the classification for issue q.
	mux.HandleFunc(get(labelsID), g.handleLabels)

	// /api/search: perform a vector similarity search.
	// POST because the arguments to the request are in the body.
	mux.HandleFunc("POST /api/search", g.handleSearchAPI)

	// /actionlog: display action log
	mux.HandleFunc(get(actionlogID), g.handleActionLog)

	// /reviews: display review dashboard
	mux.HandleFunc(get(reviewsID), g.handleReviewDashboard)

	// /dbview: view parts of the database
	mux.HandleFunc(get(dbviewID), g.handleDBview)

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
		check(g.syncGroups(ctx))

		// Embed must happen last.
		check(g.embedAll(ctx))
	}

	if flags.enablechanges {
		// Changes can run in almost any order; the labeler should
		// run before anything that uses labels.
		// Write all changes to the action log.
		check(g.fixAllComments(ctx))
		check(g.postAllRelated(ctx))
		check(g.labelAll(ctx))
		check(g.postAllRules(ctx))
		check(g.postAllBisections(ctx))
		// TODO(tatianabradley): Uncomment once ready to enable.
		// check(g.postAllOverviews(ctx))

		// Apply all actions.
		check(g.runActions())
	}

	return errs
}

// runActions runs all pending, approved actions in the Action Log.
func (g *Gaby) runActions() error {
	g.db.Lock(runActionsLock)
	defer g.db.Unlock(runActionsLock)

	return actions.Run(g.ctx, g.slog, g.db)
}

const (
	gabyGitHubSyncLock     = "gabygithubsync"
	gabyDiscussionSyncLock = "gabydiscussionsync"
	gabyGerritSyncLock     = "gabygerritsync"
	gabyGroupsSyncLock     = "gabygroupssync"
	gabyEmbedLock          = "gabyembedsync"
	gabyCrawlLock          = "gabycrawlsync"

	gabyFixCommentLock    = "gabyfixcommentaction"
	gabyPostRelatedLock   = "gabyrelatedaction"
	gabyPostRulesLock     = "gabyrulesaction"
	gabyLabelLock         = "gabylabelaction"
	gabyPostBisectionLock = "gabybisectionaction"
	runActionsLock        = "gabyrunactions"
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

func (g *Gaby) syncGroups(ctx context.Context) error {
	g.db.Lock(gabyGroupsSyncLock)
	defer g.db.Unlock(gabyGroupsSyncLock)

	// Download updated conversations from all google groups.
	if err := g.ggroups.Sync(ctx); err != nil {
		return err
	}
	// Store newly downloaded conversations in the document database.
	docs.Sync(g.docs, g.ggroups)
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

func (g *Gaby) postAllRules(ctx context.Context) error {
	g.db.Lock(gabyPostRulesLock)
	defer g.db.Unlock(gabyPostRulesLock)

	return g.rulesPoster.Run(ctx)
}

func (g *Gaby) postAllOverviews(ctx context.Context) error {
	// Hold the lock for GitHub sync because [overview.Client.Run] can't run
	// in parallel with a GitHub sync.
	g.db.Lock(gabyGitHubSyncLock)
	defer g.db.Unlock(gabyGitHubSyncLock)

	return g.overview.Run(ctx)
}

func (g *Gaby) labelAll(ctx context.Context) error {
	g.db.Lock(gabyLabelLock)
	defer g.db.Unlock(gabyLabelLock)

	return g.labeler.Run(ctx)
}

func (g *Gaby) postAllBisections(ctx context.Context) error {
	g.db.Lock(gabyPostBisectionLock)
	defer g.db.Unlock(gabyPostBisectionLock)

	// TODO: implement bisection poster. For now, just
	// log the current state of each task.
	for id, t := range g.bisect.BisectionTasks() {
		g.slog.Info("bisect.Post status", "id", id, "status", t.Status,
			"created", t.Created, "updated", t.Updated, "output", t.Output)
	}
	return nil
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

	// The following pages will be removed at the start of go1.25.
	// TODO(golang/oscar#63): remove these rules.
	"https://go.dev/doc/go1.17_spec",
	"https://go.dev/doc/go1.17_spec.html",
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

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Gaby is an experimental new bot running in the Go issue tracker as [@gabyhelp],
// to try to help automate various mundane things that a machine can do reasonably well,
// as well as to try to discover new things that a machine can do reasonably well.
//
// The name gaby is short for “Go AI Bot”, because one of the purposes of the experiment
// is to learn what LLMs can be used for effectively, including identifying what they should
// not be used for. Some of the gaby functionality will involve LLMs; other functionality will not.
// The guiding principle is to create something that helps maintainers and that maintainers like,
// which means to use LLMs when they make sense and help but not when they don't.
//
// In the long term, the intention is for this code base or a successor version
// to take over the current functionality of “gopherbot” and become [@gopherbot],
// at which point the @gabyhelp account will be retired.
//
// The [GitHub Discussion] is a good place to leave feedback about @gabyhelp.
//
// # Code Overview
//
// The bot functionality is implemented in internal packages in subdirectories.
// This comment gives a brief tour of the structure.
//
// An explicit goal for the Gaby code base is that it run well in many different environments,
// ranging from a maintainer's home server or even Raspberry Pi all the way up to a
// hosted cloud. (At the moment, Gaby runs on a Linux server in my basement.)
// Due to this emphasis on portability, Gaby defines its own interfaces for all the functionality
// it needs from the surrounding environment and then also defines a variety of
// implementations of those interfaces.
//
// Another explicit goal for the Gaby code base is that it be very well tested.
// (See my [Go Testing talk] for more about why this is so important.)
// Abstracting the various external functionality into interfaces also helps make
// testing easier, and some packages also provide explicit testing support.
//
// The result of both these goals is that Gaby defines some basic functionality
// like time-ordered indexing for itself instead of relying on some specific
// other implementation. In the grand scheme of things, these are a small amount
// of code to maintain, and the benefits to both portability and testability are
// significant.
//
// # Testing
//
// Code interacting with services like GitHub and code running on cloud servers
// is typically difficult to test and therefore undertested.
// It is an explicit requirement in this repo to test all the code,
// even (and especially) when testing is difficult.
//
// A useful command to have available when working in the code is
// [rsc.io/uncover], which prints the package source lines not covered by a unit test.
// A useful invocation is:
//
//	% go install rsc.io/uncover@latest
//	% go test && go test -coverprofile=/tmp/c.out && uncover /tmp/c.out
//	PASS
//	ok  	golang.org/x/oscar/internal/related	0.239s
//	PASS
//	coverage: 92.2% of statements
//	ok  	golang.org/x/oscar/internal/related	0.197s
//	related.go:180,181
//		p.slog.Error("triage parse createdat", "CreatedAt", issue.CreatedAt, "err", err)
//		continue
//	related.go:203,204
//		p.slog.Error("triage lookup failed", "url", u)
//		continue
//	related.go:250,251
//		p.slog.Error("PostIssueComment", "issue", e.Issue, "err", err)
//		continue
//	%
//
// The first “go test” command checks that the test passes.
// The second repeats the test with coverage enabled.
// Running the test twice this way makes sure that any syntax or type errors
// reported by the compiler are reported without coverage,
// because coverage can mangle the error output.
// After both tests pass and second writes a coverage profile,
// running “uncover /tmp/c.out” prints the uncovered lines.
//
// In this output, there are three error paths that are untested.
// In general, error paths should be tested, so tests should be written
// to cover these lines of code. In limited cases, it may not be practical
// to test a certain section, such as when code is unreachable but left
// in case of future changes or mistaken assumptions.
// That part of the code can be labeled with a comment beginning
// “// Unreachable” or “// unreachable” (usually with explanatory text following),
// and then uncover will not report it.
// If a code section should be tested but the test is being deferred to later,
// that section can be labeled “// Untested” or “// untested” instead.
//
// The [golang.org/x/oscar/internal/testutil] package provides a few other testing helpers.
//
// The overview of the code now proceeds from bottom up, starting with
// storage and working up to the actual bot.
//
// # Secret Storage
//
// Gaby needs to manage a few secret keys used to access services.
// The [golang.org/x/oscar/internal/secret] package defines the interface for
// obtaining those secrets. The only implementations at the moment
// are an in-memory map and a disk-based implementation that reads
// $HOME/.netrc. Future implementations may include other file formats
// as well as cloud-based secret storage services.
//
// Secret storage is intentionally separated from the main database storage,
// described below. The main database should hold public data, not secrets.
//
// # Large Language Models
//
// Gaby defines the interface it expects from a large language model.
//
// The [llm.Embedder] interface abstracts an LLM that can take a collection
// of documents and return their vector embeddings, each of type [llm.Vector].
// The only real implementation to date is [golang.org/x/oscar/internal/gemini].
// It would be good to add an offline implementation using Ollama as well.
//
// For tests that need an embedder but don't care about the quality of
// the embeddings, [llm.QuoteEmbedder] copies a prefix of the text
// into the vector (preserving vector unit length) in a deterministic way.
// This is good enough for testing functionality like vector search
// and simplifies tests by avoiding a dependence on a real LLM.
//
// At the moment, only the embedding interface is defined.
// In the future we expect to add more interfaces around text generation
// and tool use.
//
// # Storage
//
// As noted above, Gaby defines interfaces for all the functionality it needs
// from its external environment, to admit a wide variety of implementations
// for both execution and testing. The lowest level interface is storage,
// defined in [golang.org/x/oscar/internal/storage].
//
// Gaby requires a key-value store that supports ordered traversal of key ranges
// and atomic batch writes up to a modest size limit (at least a few megabytes).
// The basic interface is [storage.DB].
// [storage.MemDB] returns an in-memory implementation useful for testing.
// Other implementations can be put through their paces using
// [storage.TestDB].
//
// The only real [storage.DB] implementation is [golang.org/x/oscar/internal/pebble],
// which is a [LevelDB]-derived on-disk key-value store developed and used
// as part of [CockroachDB]. It is a production-quality local storage implementation
// and maintains the database as a directory of files.
//
// In the future we plan to add an implementation using [Google Cloud Firestore],
// which provides a production-quality key-value lookup as a Cloud service
// without fixed baseline server costs.
// (Firestore is the successor to Google Cloud Datastore.)
//
// The [storage.DB] makes the simplifying assumption that storage never fails,
// or rather that if storage has failed then you'd rather crash your program than
// try to proceed through typically untested code paths.
// As such, methods like Get and Set do not return errors.
// They panic on failure, and clients of a DB can call the DB's Panic method
// to invoke the same kind of panic if they notice any corruption.
// It remains to be seen whether this decision is kept.
//
// In addition to the usual methods like Get, Set, and Delete, [storage.DB] defines
// Lock and Unlock methods that acquire and release named mutexes managed
// by the database layer. The purpose of these methods is to enable coordination
// when multiple instances of a Gaby program are running on a serverless cloud
// execution platform. So far Gaby has only run on an underground basement server
// (the opposite of cloud), so these have not been exercised much and the APIs
// may change.
//
// In addition to the regular database, package storage also defines [storage.VectorDB],
// a vector database for use with LLM embeddings.
// The basic operations are Set, Get, and Search.
// [storage.MemVectorDB] returns an in-memory implementation that
// stores the actual vectors in a [storage.DB] for persistence but also
// keeps a copy in memory and searches by comparing against all the vectors.
// When backed by a [storage.MemDB], this implementation is useful for testing,
// but when backed by a persistent database, the implementation suffices for
// small-scale production use (say, up to a million documents, which would
// require 3 GB of vectors).
//
// It is possible that the package ordering here is wrong and that VectorDB
// should be defined in the llm package, built on top of storage,
// and not the current “storage builds on llm”.
//
// # Ordered Keys
//
// Because Gaby makes minimal demands of its storage layer,
// any structure we want to impose must be implemented on top of it.
// Gaby uses the [rsc.io/ordered] encoding format to produce database keys
// that order in useful ways.
//
// For example, ordered.Encode("issue", 123) < ordered.Encode("issue", 1001),
// so that keys of this form can be used to scan through issues in numeric order.
// In contrast, using something like fmt.Sprintf("issue%d", n) would visit issue 1001
// before issue 123 because "1001" < "123".
//
// Using this kind of encoding is common when using NoSQL key-value storage.
// See the [rsc.io/ordered] package for the details of the specific encoding.
//
// # Timed Storage
//
// One of the implied jobs Gaby has is to collect all the relevant information
// about an open source project: its issues, its code changes, its documentation,
// and so on. Those sources are always changing, so derived operations like
// adding embeddings for documents need to be able to identify what is new
// and what has been processed already. To enable this, Gaby implements
// time-stamped—or just “timed”—storage, in which a collection of key-value pairs
// also has a “by time” index of ((timestamp, key), no-value) pairs to make it possible
// to scan only the key-value pairs modified after the previous scan.
// This kind of incremental scan only has to remember the last timestamp processed
// and then start an ordered key range scan just after that timestamp.
//
// This convention is implemented by [golang.org/x/oscar/internal/timed], along with
// a [timed.Watcher] that formalizes the incremental scan pattern.
//
// # Document Storage
//
// Various package take care of downloading state from issue trackers and the like,
// but then all that state needs to be unified into a common document format that
// can be indexed and searched. That document format is defined by
// [golang.org/x/oscar/internal/docs]. A document consists of an ID (conventionally a URL),
// a document title, and document text. Documents are stored using timed storage,
// enabling incremental processing of newly added documents .
//
// # Document Embedding
//
// The next stop for any new document is embedding it into a vector and storing
// that vector in a vector database. The [golang.org/x/oscar/internal/embeddocs] package
// does this, and there is very little to it, given the abstractions of a document store
// with incremental scanning, an LLM embedder, and a vector database, all of which
// are provided by other packages.
//
// # HTTP Record and Replay
//
// None of the packages mentioned so far involve network operations, but the
// next few do. It is important to test those but also equally important not to
// depend on external network services in the tests. Instead, the package
// [golang.org/x/oscar/internal/httprr] provides an HTTP record/replay system specifically
// designed to help testing. It can be run once in a mode that does use external
// network servers and records the HTTP exchanges, but by default tests look up
// the expected responses in the previously recorded log, replaying those responses.
//
// The result is that code making HTTP request can be tested with real server
// traffic once and then re-tested with recordings of that traffic afterward.
// This avoids having to write entire fakes of services but also avoids needing
// the services to stay available in order for tests to pass. It also typically makes
// the tests much faster than using the real servers.
//
// # GitHub Interactions
//
// Gaby uses GitHub in two main ways. First, it downloads an entire copy of the
// issue tracker state, with incremental updates, into timed storage.
// Second, it performs actions in the issue tracker, like editing issues or comments,
// applying labels, or posting new comments. These operations are provided by
// [golang.org/x/oscar/internal/github].
//
// Gaby downloads the issue tracker state using GitHub's REST API, which makes
// incremental updating very easy but does not provide access to a few newer features
// such as project boards and discussions, which are only available in the GraphQL API.
// Sync'ing using the GraphQL API is left for future work: there is enough data available
// from the REST API that for now we can focus on what to do with that data and not
// that a few newer GitHub features are missing.
//
// The github package provides two important aids for testing. For issue tracker state,
// it also allows loading issue data from a simple text-based issue description, avoiding
// any actual GitHub use at all and making it easier to modify the test data.
// For issue tracker actions, the github package defaults in tests to not actually making
// changes, instead diverting edits into an in-memory log. Tests can then check the log
// to see whether the right edits were requested.
//
// The [golang.org/x/oscar/internal/githubdocs] package takes care of adding content from
// the downloaded GitHub state into the general document store.
// Currently the only GitHub-derived documents are one document per issue,
// consisting of the issue title and body. It may be worth experimenting with
// incorporating issue comments in some way, although they bring with them
// a significant amount of potential noise.
//
// # Gerrit Interactions
//
// Gaby will need to download and store Gerrit state into the database and then
// derive documents from it. That code has not yet been written, although
// [rsc.io/gerrit/reviewdb] provides a basic version that can be adapted.
//
// # Web Crawling
//
// Gaby will also need to download and store project documentation into the
// database and derive documents from it corresponding to cutting the page
// at each heading. That code has been written but is not yet tested well enough
// to commit. It will be added later.
//
// # Fixing Comments
//
// The simplest job Gaby has is to go around fixing new comments, including
// issue descriptions (which look like comments but are a different kind of GitHub data).
// The [golang.org/x/oscar/internal/commentfix] package implements this,
// watching GitHub state incrementally and applying a few kinds of rewrite rules
// to each new comment or issue body.
// The commentfix package allows automatically editing text, automatically editing URLs,
// and automatically hyperlinking text.
//
// # Finding Related Issues and Documents
//
// The next job Gaby has is to respond to new issues with related issues and documents.
// The [golang.org/x/oscar/internal/related] package implements this,
// watching GitHub state incrementally for new issues, filtering out ones that should be ignored,
// and then finding related issues and documents and posting a list.
//
// This package was originally intended to identify and automatically close duplicates,
// but the difference between a duplicate and a very similar or not-quite-fixed issue
// is too difficult a judgement to make for an LLM. Even so, the act of bringing forward
// related context that may have been forgotten or never known by the people reading
// the issue has turned out to be incredibly helpful.
//
// # Main Loop
//
// All of these pieces are put together in the main program, this package, [golang.org/x/oscar].
// The actual main package has no tests yet but is also incredibly straightforward.
// It does need tests, but we also need to identify ways that the hard-coded policies
// in the package can be lifted out into data that a natural language interface can
// manipulate. For example the current policy choices in package main amount to:
//
//	cf := commentfix.New(lg, gh, "gerritlinks")
//	cf.EnableProject("golang/go")
//	cf.EnableEdits()
//	cf.AutoLink(`\bCL ([0-9]+)\b`, "https://go.dev/cl/$1")
//	cf.ReplaceURL(`\Qhttps://go-review.git.corp.google.com/\E`, "https://go-review.googlesource.com/")
//
//	rp := related.New(lg, db, gh, vdb, dc, "related")
//	rp.EnableProject("golang/go")
//	rp.EnablePosts()
//	rp.SkipBodyContains("— [watchflakes](https://go.dev/wiki/Watchflakes)")
//	rp.SkipTitlePrefix("x/tools/gopls: release version v")
//	rp.SkipTitleSuffix(" backport]")
//
// These could be stored somewhere as data and manipulated and added to by the LLM
// in response to prompts from maintainers. And other features could be added and
// configured in a similar way. Exactly how to do this is an important thing to learn in
// future experimentation.
//
// # Future Work and Structure
//
// As mentioned above, the two jobs Gaby does already are both fairly simple and straightforward.
// It seems like a general approach that should work well is well-written, well-tested deterministic
// traditional functionality such as the comment fixer and related-docs poster, configured by
// LLMs in response to specific directions or eventually higher-level goals specified by project
// maintainers. Other functionality that is worth exploring is rules for automatically labeling issues,
// rules for identifying issues or CLs that need to be pinged, rules for identifying CLs that need
// maintainer attention or that need submitting, and so on. Another stretch goal might be to identify
// when an issue needs more information and ask for that information. Of course, it would be very
// important not to ask for information that is already present or irrelevant, so getting that right would
// be a very high bar. There is no guarantee that today's LLMs work well enough to build a useful
// version of that.
//
// Another important area of future work will be running Gaby on top of cloud databases
// and then moving Gaby's own execution into the cloud. Getting it a server with a URL will
// enable GitHub callbacks instead of the current 2-minute polling loop, which will enable
// interactive conversations with Gaby.
//
// Overall, we believe that there are a few good ideas for ways that LLM-based bots can help
// make project maintainers' jobs easier and less monotonous, and they are waiting to be found.
// There are also many bad ideas, and they must be filtered out. Understanding the difference
// will take significant care, thought, and experimentation. We have work to do.
//
// [@gabyhelp]: https://github.com/gabyhelp
// [@gopherbot]: https://github.com/gopherbot
// [GitHub Discussion]: https://github.com/golang/go/discussions/67901
// [LevelDB]: https://github.com/google/leveldb
// [CockroachDB]: https://github.com/cockroachdb/cockroach
// [Google Cloud Firestore]: https://cloud.google.com/firestore
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

	"golang.org/x/oscar/internal/commentfix"
	"golang.org/x/oscar/internal/docs"
	"golang.org/x/oscar/internal/embeddocs"
	"golang.org/x/oscar/internal/gemini"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/githubdocs"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/pebble"
	"golang.org/x/oscar/internal/related"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
)

var searchMode = flag.Bool("search", false, "run in interactive search mode")

func main() {
	flag.Parse()

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
	ai, err := gemini.NewClient(lg, sdb, http.DefaultClient)
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

	gh.Sync()
	githubdocs.Sync(lg, dc, gh)
	embeddocs.Sync(lg, vdb, ai, dc)

	cf := commentfix.New(lg, gh, "gerritlinks")
	cf.EnableProject("golang/go")
	cf.EnableEdits()
	cf.AutoLink(`\bCL ([0-9]+)\b`, "https://go.dev/cl/$1")
	cf.ReplaceURL(`\Qhttps://go-review.git.corp.google.com/\E`, "https://go-review.googlesource.com/")

	rp := related.New(lg, db, gh, vdb, dc, "related")
	rp.EnableProject("golang/go")
	rp.EnablePosts()
	rp.SkipBodyContains("— [watchflakes](https://go.dev/wiki/Watchflakes)")
	rp.SkipTitlePrefix("x/tools/gopls: release version v")
	rp.SkipTitleSuffix(" backport]")
	for {
		gh.Sync()
		githubdocs.Sync(lg, dc, gh)
		embeddocs.Sync(lg, vdb, ai, dc)
		cf.Run()
		rp.Run()
		time.Sleep(2 * time.Minute)
	}
	return
}

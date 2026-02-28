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
// hosted cloud. Gaby currently runs on Google Cloud.
// Due to this emphasis on portability, Gaby defines its own interfaces for all the functionality
// it needs from the surrounding environment and then also defines a variety of
// implementations of those interfaces.
//
// Another explicit goal for the Gaby code base is that it be very well tested.
// (See my [Go Testing talk] for more about why this is so important.)
// Abstracting the various external functionality into interfaces also helps make
// testing easier, and some packages also provide explicit testing support.
//
// A third goal is "human-in-the-loop" operation. Most of Gaby's actions are
// recorded in a persistent action log (see [golang.org/x/oscar/internal/actions])
// where they can be reviewed and approved by a human before being executed.
//
// The result of these goals is that Gaby defines some basic functionality
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
// obtaining those secrets.
// Implementations include an in-memory map, a disk-based implementation
// that reads $HOME/.netrc, and a [Google Cloud Secret Manager] implementation
// (see [golang.org/x/oscar/internal/gcp/gcpsecret]).
//
// Secret storage is intentionally separated from the main database storage,
// described below. The main database should hold public data, not secrets.
//
// # Large Language Models
//
// Gaby defines the interfaces it expects from a large language model.
//
// The [llm.Embedder] interface abstracts an LLM that can take a collection
// of documents and return their vector embeddings, each of type [llm.Vector].
// The [llm.ContentGenerator] interface abstracts an LLM that can generate
// text in response to a sequence of messages.
//
// The primary implementation is [golang.org/x/oscar/internal/gcp/gemini],
// which uses [Google Gemini]. Other implementations include
// [golang.org/x/oscar/internal/ollama].
//
// For tests that need an embedder but don't care about the quality of
// the embeddings, [llm.QuoteEmbedder] copies a prefix of the text
// into the vector (preserving vector unit length) in a deterministic way.
// This is good enough for testing functionality like vector search
// and simplifies tests by avoiding a dependence on a real LLM.
//
// The [golang.org/x/oscar/internal/llmapp] package provides a high-level
// application layer on top of these interfaces, including support for
// prompt templates.
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
// The real [storage.DB] implementations are:
//
//   - [golang.org/x/oscar/internal/pebble], which is a [LevelDB]-derived
//     on-disk key-value store developed and used as part of [CockroachDB].
//     It is a production-quality local storage implementation.
//   - [Google Cloud Firestore], which provides a production-quality
//     key-value lookup as a Cloud service without fixed baseline server costs.
//     Its implementation is in [golang.org/x/oscar/internal/gcp/firestore].
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
// execution platform.
//
// In addition to the regular database, package storage also defines [storage.VectorDB],
// a vector database for use with LLM embeddings.
// The basic operations are Set, Get, and Search.
// [storage.MemVectorDB] returns an in-memory implementation that
// stores the actual vectors in a [storage.DB] for persistence but also
// keeps a copy in memory and searches by comparing against all the vectors.
// There is also a [Google Cloud Firestore] implementation in
// [golang.org/x/oscar/internal/gcp/firestore].
//
// It is possible that the package ordering here is wrong and that VectorDB
// should be defined in the llm package, built on top of storage,
// and not the current “storage builds on llm”.
//
// # Overlay Databases
//
// For local development and testing, Gaby supports an "overlay" database
// (see [golang.org/x/oscar/internal/storage/NewOverlayDB]).
// An overlay database combines an "overlay" DB (typically a [storage.MemDB])
// with a "base" DB (typically a production [Google Cloud Firestore] database).
// All writes go to the overlay, while reads check the overlay first and
// then fall back to the base DB if the key is not found.
//
// This allows a developer to run Gaby locally against production data
// without risk of modifying the production database. The `-overlay` flag
// in the main Gaby program enables this mode.
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
// Various packages take care of downloading state from issue trackers and the like,
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
// Gaby downloads and stores Gerrit state into the database and
// derives documents from it. These operations are provided by
// [golang.org/x/oscar/internal/gerrit].
//
// # Web Crawling
//
// Gaby downloads and stores project documentation into the
// database and derives documents from it corresponding to each page.
// The [golang.org/x/oscar/internal/crawl] package implements the
// web crawler.
//
// # Google Groups Interactions
//
// Gaby also downloads and stores Google Groups conversations.
// The [golang.org/x/oscar/internal/googlegroups] package implements
// this functionality.
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
// # Rules and Labels
//
// Gaby can identify violations of project rules and automatically
// label issues.
// The [golang.org/x/oscar/internal/rules] package checks issues
// against a set of natural language rules using an LLM.
// The [golang.org/x/oscar/internal/labels] package suggests labels
// for issues based on their content.
//
// # Overviews
//
// Gaby can generate overviews of issues and discussions to help
// maintainers quickly understand the state of a thread.
// The [golang.org/x/oscar/internal/overview] package implements this.
//
// # Bisection
//
// Gaby can perform automatic bisection of regressions.
// The [golang.org/x/oscar/internal/bisect] package manages bisection
// tasks, which are typically executed as asynchronous jobs in the cloud.
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
// Gaby's current functionality includes deterministic traditional
// functionality such as the comment fixer, configured by LLMs in response
// to specific directions or higher-level goals specified by project
// maintainers.
//
// Future work includes improving the natural language interface
// for configuring Gaby, and adding more functionality like
// identifying CLs that need maintainer attention.
// Another area of exploration is interactive conversations with Gaby
// enabled by GitHub callbacks.
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
// [Google Cloud Secret Manager]: https://cloud.google.com/secret-manager
// [Google Gemini]: https://ai.google.dev/
// [Go Testing talk]: https://research.swtch.com/testing
package main

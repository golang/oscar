# Oscar, an open-source contributor agent architecture

Oscar is a project aiming to improve open-source software development
by creating automated help, or “agents,” for open-source maintenance.
We believe there are many opportunities to reduce the
amount of toil involved with maintaining open-source projects
both large and small.

The ability of large language models (LLMs) to do semantic analysis of
natural language (such as issue reports or maintainer instructions)
and to convert between natural language instructions and program code
creates new opportunities for agents to interact more smoothly with people.
LLMs will likely end up being only a small (but critical!) part of the picture;
the bulk of an agent's actions will be executing standard, deterministic code.

Oscar differs from many development-focused uses of LLMs by not trying
to augment or displace the code writing process at all.
After all, writing code is the fun part of writing software.
Instead, the idea is to focus on the not-fun parts, like processing incoming issues,
matching questions to existing documentation, and so on.

Oscar is very much an experiment. We don't know yet where it will go or what
we will learn. Even so, our first prototype,
the [@gabyhelp](https://github.com/gabyhelp) bot, has already had many
[successful interactions in the Go issue tracker](https://github.com/golang/go/issues?q=label%3Agabywins).

For now, Oscar is being developed under the auspices of the Go project.
At some point in the future it may (or may not) be spun out into a separate project.

The rest of this README explains Oscar in more detail.

## Goals

The concrete goals for the Oscar project are:

  - Reduce maintainer effort to resolve issues
    [note that resolve does not always mean fix]
  - Reduce maintainer effort to resolve change lists (CLs) or pull requests (PRs)
    [note that resolve does not always mean submit/merge]
  - Reduce maintainer effort to resolve forum questions
  - Enable more people to become productive maintainers

It is a non-goal to automate away coding.
Instead we are focused on automating away maintainer toil.

## Approach

Maintainer toil is not unique to the Go project, so we are aiming to build
an architecture that any software project can reuse and extend,
building their own agents customized to their project's needs.
Hence Oscar: _open-source contributor agent architecture_.
Exactly what that will mean is still something we are exploring.

So far, we have identified three capabilities that will be an important part
of Oscar:

 1. Indexing and surfacing related project context during
    contributor interactions.
 2. Using natural language to control deterministic tools.
 3. Analyzing issue reports and CLs/PRs, to help improve them
    in real time during or shortly after submission,
    and to label and route them appropriately.

It should make sense that LLMs have something to offer here,
because open-source maintenance is fundamentally about
interacting with people using natural language, and
natural language is what LLMs are best at.
So it's not surprising that all of these have an LLM-related component.
On the other hand, all of these are also backed by
significant amounts of deterministic code.
Our approach is to use LLMs for what they're good at—semantic
analysis of natural language and translation from
natural language into programs—and
rely on deterministic code to do the rest.

The following sections look at each of those three important capabilities in turn.
Note that we are still experimenting,
and we expect to identify additional important capabilities as time goes on.

### Indexing and surfacing related project context

Software projects are complex beasts.
Only at the very beginning can a maintainer expect
to keep all the important details and context in their head,
and even when that's possible, those being in one person's
head does not help when a new contributor arrives with
a bug report, a feature request, or a question.
To address this, maintainers write design documentation,
API references, FAQs, manual pages, blog posts, and so on.
Now, instead of providing context directly, a maintainer can
provide links to written context that already exists.
Serving as a project search engine is still not the best use of
the maintainer's time.
Once a project grows even to modest size, any single maintainer
cannot keep track of all the context that might be relevant,
making it even harder to serve as a project search engine.

On the other hand, LLMs turn out to be a great platform for
building a project search engine.
LLMs can analyze documents and produce _embeddings_,
which are high-dimensional (for example, 768-dimensional)
floating point unit vectors with the property that documents
with similar semantic meaning are mapped to vectors that point in similar directions.
(For more about embeddings, see
[this blog post](https://cloud.google.com/blog/topics/developers-practitioners/meet-ais-multitool-vector-embeddings).)
Combined with a vector database to retrieve vectors similar
to an input vector,
LLM embeddings provide a very effective way to index
all of an open-source project's context, including
documentation, issue reports, and CLs/PRs, and forum discussions.
When a new issue report arrives, an agent can use the LLM-based
project context index to identify highly related context,
such as similar previous issues or relevant project documentation.

Our prototype agent implements this functionality and replies to
new issues in the Go repository with a list of at most ten
highly related links that add context to the report.
(If the agent cannot find anything that looks related enough,
it stays quiet and does not reply at all.)
In the first few weeks we ran the agent, we identified the following
benefits of such an agent:

 1. **The agent surfaces related context to contributors.**

    It is common for new issue reports to duplicate existing issue reports:
    a new bug might be reported multiple times in a short time window,
    or a non-bug might be reported every few months.
    When an agent replies with a link to a duplicate report,
    the contributor can close their new report and then watch that earlier issue.
    When an agent replies with a link to a report that looks like a duplicate
    but is not, the contributor can provide added context to distinguish their
    report from the earlier one.

    For example, in [golang/go#68196](https://github.com/golang/go/issues/68196),
    after the agent replied with a near duplicate, the original reporter commented:

    > Good bot :). Based on the discussion in this issue, I understand that
    > it might not be possible to do what's being suggested here.
    > If that's the case I'd still suggest to leave the issue open for a bit
    > to see how many Go users care about this problem.

    As another example, on [golang/go#67986](https://github.com/golang/go/issues/67986),
    after the agent replied with an exact duplicate, the original reporter commented:

    > Drats, I spent quite a bit of time searching existing issues. Not sure how I missed [that one].

 2. **The agent surfaces related context even to project maintainers.**

    Once a project reaches even modest size, no one person can remember all the context,
    not even a highly dedicated project maintainer.
    When an agent replies with a link to a related report,
    that eliminates the time the maintainer must spend to find it.
    If the maintainer has forgotten the related report entirely,
    or never saw it in the first place (perhaps it was handled by someone else),
    the reply is even more helpful, because it can point the maintainer
    in the right direction and save them the effort of repeating the
    analysis done in the earlier issue.

    For example, in [golang/go#68183](https://github.com/golang/go/issues/68183),
    a project maintainer filed a
    bug against the Go compiler for mishandling certain malformed identifiers.
    The agent replied with a link to a similar report of the same bug,
    filed almost four years earlier but triaged to low priority.
    The added context allowed closing the earlier bug and
    provided an argument for raising the priority of the new bug.

    As another example, in [golang/go#67938](https://github.com/golang/go/issues/67938),
    a project maintainer filed a bug against the Go coverage tool
    for causing the compiler to report incorrect sub-line position information.
    The agent replied with an earlier related issue (incorrect line numbers)
    from a decade earlier
    as well as a more recent issue about coverage
    not reporting sub-line position information at all.
    The first bug was important context,
    and the second bug's “fix” was the root cause of the bug in the new report:
    the sub-line position information added then was not added correctly.
    Those links pinpointed the exact code where the bug was.
    Once that was identified, it was also easy to determine the fix.

 3. **The agent interacts with bug reporters immediately.**

    In all of the previous examples, the fact that the agent replied only a minute or two
    after the report was filed meant that the reporter was still available and engaged
    enough to respond in a meaningful way: adding details to clarify the suggestion,
    closing the report as a duplicate, raising bug priority based on past reports,
    or identifying a fix.
    In contrast, if hours or days (or more) go by after the initial report,
    the original reporter may no longer be available, interested, or able
    to provide context or additional details.
    Immediately after the bug report is the best time to engage the reporter
    and refine the report.
    Maintainers cannot be expected to be engaged in this work all the time,
    but an agent can.

Finally, note that surfacing project context is extensible,
so that projects can incorporate their context no matter what form it takes.
Our prototype's context sources are tailored to the Go project,
reading issues from GitHub, documentation from [go.dev](https://go.dev),
and (soon) code reviews from Gerrit,
but the architecture makes it easy to add additional sources.

### Using natural language to control deterministic tools

The second important agent capability is using natural
language to control deterministic tooling.
As open-source projects grow, the number of helpful tools increases,
and it can be difficult to keep track of all of them and remember
how to use each one.
For example, our prototype includes a general facility
for editing GitHub issue comments to add or fix links.
We envision also adding facilities for adding labels to
an issue or assigning or CC'ing people
when it matches certain criteria.
If a maintainer does not know this functionality exists
it might be difficult to find.
And even if they know it exists, perhaps they aren't familiar
with the specific API and don't want to take the time to learn it.

On the other hand, LLMs are very good at translating between
intentions written in natural language
and executable forms of those intentions such as program code
or tool invocations.
We have done preliminary experiments with Gemini selecting from
and invoking available tools to satisfy natural language requests
made by a maintainer.
We don't have anything running for real yet,
but it looks like a promising approach.

A different approach would be to rely more heavily on LLMs,
letting them edit code, issues, and so on entirely based on
natural language prompts with no deterministic tools.
This “magic wand” approach demands more of LLMs than they
are capable of today.
We believe it will be far more effective to use LLMs to convert
from natural language to deterministic tool use once
and then apply those deterministic tools automatically.
Our approach also limits the amount of “LLM supervision” needed:
a person can check that the tool invocation is correct
and then rely on the tool to operate deterministically.

We have not built this part of Oscar yet, but when we do,
it will be extensible, so that projects can easily plug in their own tools.

### Analyzing issue reports and CLs/PRs

The third important agent capability is analyzing issue reports
and CLs/PRs (change lists / pull requests).
Posting about related issues is a limited form of analysis,
but we plan to add other kinds of semantic analysis,
such as determining that an issue is primarily about performance
and should have a “performance” label added.

We also plan to explore whether it is possible to analyze reports
well enough to identify whether more information is needed to
make the report useful. For example, if a report does not include
a link to a reproduction program on the [Go playground](https://go.dev/play),
the agent could ask for one.
And if there is such a link, the agent could make sure to inline the code
into the report to make it self-contained.
The agent could potentially also run a sandboxed execution tool
to identify which Go releases contain the bug and even use `git bisect`
to identify the commit that introduced the bug.

As discussed earlier, all of these analyses and resulting interactions
work much better when they happen immediately after the report
is filed, when the reporter is still available and engaged.
Automated agents can be on duty 24/7.

We have not built this part of Oscar yet, but when we do,
it too will be extensible, so that projects can easily define their own
analyses customized to the reports they receive.

## Prototype

Our first prototype to explore open-source contributor agents is called Gaby (for “Go AI bot”)
and runs in the [Go issue tracker](https://github.com/golang/go/issues),
posting as [@gabyhelp](https://github.com/gabyhelp).
The source code is in [internal/gaby](internal/gaby) in this repository.
The [gaby package's documentation](https://pkg.go.dev/golang.org/x/oscar/internal/gaby)
explains the overall structure of the code in the repository as well.

So far, Gaby indexes Go issue content from GitHub
as well as Go documentation from [go.dev](https://go.dev)
and replies to new issues with relevant links.
We plan to add Gerrit code reviews in the near future.

Gaby's structure makes it easy to run on any kind of hosting service,
using any LLM, any storage layer, and any vector database.
Right now, it runs on a local workstation, using Google's Gemini LLM,
[Pebble](https://github.com/cockroachdb/pebble) key-value storage files,
and an in-memory vector database.

We plan to add support for a variety of other options, including
[Google Cloud Firestore](https://firebase.google.com/docs/firestore)
for key-value storage and vector database.
Firestore in particular will make it easy to run Gaby on hosted platforms
like [Cloud Run](https://cloud.google.com/run).

Running on hosted platforms with their own URLs
(as opposed to a local workstation)
will enable subscribing to
[GitHub webhooks](https://docs.github.com/en/webhooks/about-webhooks),
so that Gaby can respond even more quickly to issues
and also carry on conversations.

Our experience with all of this will inform the eventual generalized Oscar design.

There is much work left to do.

## Relationship to Gopherbot

The Go project has run its own completely deterministic agent,
[@gopherbot](https://github.com/gopherbot), for many years.
That agent is configured by writing, reviewing, and checking in Go code in the
[golang.org/x/build/cmd/gopherbot](https://pkg.go.dev/golang.org/x/build/cmd/gopherbot)
package.
Having the agent has been an incredible help to the Go project
and is part of the inspiration for Oscar.
At the same time, we are aiming for an even lighter-weight
way to configure new agent behaviors: using natural language
to control general behaviors.
Over time, our goal is to merge @gabyhelp back into @gopherbot
by re-building @gopherbot as an Oscar agent.

## Discussion and Feedback

We are excited about the opportunities here, but we recognize
that we may be missing important concerns as well as
important opportunities to reduce open-source maintainer toil.
We have created [this GitHub discussion](https://github.com/golang/go/discussions/68490) to discuss
both concerns and new ideas for ways that Oscar-based agent can
help improve open-source maintenance.
Feedback there is much appreciated.

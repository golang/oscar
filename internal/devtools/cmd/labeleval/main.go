// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Labeleval is a program for evaluating issue categorization.
It applies the internal/labels package to a selected set of issues
and compares the results with expected values.

Usage:

	labeleval categoryconfig issueconfig

Categoryconfig defines the list of categories to use.
It is a JSON file that matches the type

	struct {
	  Categories []labels.Category
	}

Issueconfig is a list of issue numbers to evaluate, along with their expected category.
The issues must all be in the production DB under the golang/go project.

By default, all the issues in the issueconfig file are evaluated.
The -run flag provides a way to run a subset of them.

There are four results of evaluating an issue:

	PASS:  the LLM chose a category that matches the desired one
	FAIL:  the LLM chose a different category
	NEW:   the expected category is not in the category config
	ERROR: there was a failure trying to run the LLM

A typical run will use the category config from the internal/labels package and the list
of issues in this package. From repo root:

	go run ./internal/devtools/cmd/labeleval internal/labels/static/categories.json internal/devtools/cmd/labeleval/issues.txt
*/
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oscar/internal/gcp/firestore"
	"golang.org/x/oscar/internal/gcp/gemini"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/labels"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/sync/errgroup"
)

var runIssueNumbers = flag.String("run", "", "comma-separated issue numbers to run")

func usage() {
	fmt.Fprintf(os.Stderr, "usage: labeleval categoryconfig issueconfig\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("labeleval: ")
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() != 2 {
		usage()
	}
	if err := run(context.Background(), flag.Arg(0), flag.Arg(1)); err != nil {
		log.Fatal(err)
	}
}

// An issueConfig refers to an issue in the golang/go project, with its expected category.
type issueConfig struct {
	Number       int
	WantCategory string
}

// A reponse holds the return values from [labels.IssueCategoryFromList].
type response struct {
	cat         labels.Category
	explanation string
	err         error
}

func run(ctx context.Context, categoryconfigFile, issueConfigFile string) error {
	var categoryConfig struct {
		Categories []labels.Category
	}

	if err := readJSONFile(categoryconfigFile, &categoryConfig); err != nil {
		return err
	}
	knownCategories := map[string]bool{}
	for _, c := range categoryConfig.Categories {
		knownCategories[c.Name] = true
	}

	allIssueConfigs, err := readIssueFile(issueConfigFile)
	if err != nil {
		return err
	}
	var issueConfigs []issueConfig
	if len(*runIssueNumbers) == 0 {
		issueConfigs = allIssueConfigs
	} else {
		// Filter by the provided issue numbers.
		rns := strings.Split(*runIssueNumbers, ",")
		runNums := map[int]bool{}
		for _, rn := range rns {
			n, err := strconv.Atoi(strings.TrimSpace(rn))
			if err != nil {
				return err
			}
			runNums[n] = true
		}
		for _, ic := range allIssueConfigs {
			if runNums[ic.Number] {
				issueConfigs = append(issueConfigs, ic)
			}
		}
	}

	lg := slog.New(slog.NewTextHandler(os.Stderr, nil))
	db, err := firestore.NewDB(ctx, lg, "oscar-go-1", "prod")
	if err != nil {
		return err
	}
	cgen, err := newGeminiClient(ctx, lg)
	if err != nil {
		return err
	}

	responses := make([]response, len(issueConfigs))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(10)
	log.Printf("evaluating %d issues", len(issueConfigs))
	start := time.Now()
	for i, ic := range issueConfigs {
		g.Go(func() error {
			issue, err := github.LookupIssue(db, "golang/go", int64(ic.Number))
			if err != nil {
				return err
			}
			got, exp, err := labels.IssueCategoryFromList(gctx, cgen, issue, categoryConfig.Categories)
			responses[i] = response{got, exp, err}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	log.Printf("finished in %.1f seconds", time.Since(start).Seconds())

	for i, ic := range issueConfigs {
		res := responses[i]
		fmt.Printf("%-6d ", ic.Number)
		if res.err != nil {
			fmt.Printf("ERROR %v\n", res.err)
		} else if !knownCategories[ic.WantCategory] {
			fmt.Printf("NEW got %s; want %s is not in list of known categories\n",
				res.cat.Name, ic.WantCategory)
		} else if res.cat.Name != ic.WantCategory {
			fmt.Printf("FAIL got %s want %s\n", res.cat.Name, ic.WantCategory)
			fmt.Printf("%11s %s\n", "exp", res.explanation)
		} else {
			fmt.Printf("PASS\n")
		}
	}
	return nil
}

func newGeminiClient(ctx context.Context, lg *slog.Logger) (*gemini.Client, error) {
	sdb := secret.Netrc()
	return gemini.NewClient(ctx, lg, sdb, http.DefaultClient,
		gemini.DefaultEmbeddingModel, gemini.DefaultGenerativeModel)
}

func readJSONFile(filename string, p any) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	return dec.Decode(p)
}

func readIssueFile(filename string) ([]issueConfig, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var issues []issueConfig
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		// Trim comment from end of line.
		if i := strings.LastIndexByte(line, '#'); i > 0 {
			line = line[:i]
		}
		num, rest, found := strings.Cut(line, " ")
		if !found {
			return nil, fmt.Errorf("%s:%d: missing want", filename, i+1)
		}
		n, err := strconv.Atoi(num)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %v", filename, i+1, err)
		}
		issues = append(issues, issueConfig{Number: n, WantCategory: strings.TrimSpace(rest)})
	}
	return issues, nil
}

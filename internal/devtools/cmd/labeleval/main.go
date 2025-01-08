// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Labeleval is a program for evaluating issue categorization.
It applies the internal/labels package to a selected set of issues
and compares the results with expected values.

Usage:

	labeleval project issueconfig

Project is used to obtain the lists of categories and examples used by internal/labels.

Issueconfig is a list of issue numbers to evaluate, along with their expected category.
The issues must all be in the production DB under the golang/go project.

By default, all the issues in the issueconfig file are evaluated.
The -run flag provides a way to run a subset of them.

There are four results of evaluating an issue:

	PASS:  the LLM chose a category that matches the desired one
	FAIL:  the LLM chose a different category
	NEW:   the expected category is not in the project's list.
	ERROR: there was a failure trying to run the LLM

A typical run will use the golang/go project and the list of issues in this package.
From this directory:

	go run . golang/go issues.txt
*/
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oscar/internal/gcp/firestore"
	"golang.org/x/oscar/internal/gcp/gemini"
	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/labels"
	"golang.org/x/oscar/internal/secret"
	"golang.org/x/oscar/internal/storage"
	"golang.org/x/sync/errgroup"
	"gopkg.in/yaml.v3"
)

var (
	runIssueNumbers = flag.String("run", "", "comma-separated issue numbers to run")
	count           = flag.Int("count", 1, "repeats of the evaluation")
)

func usage() {
	fmt.Fprintf(os.Stderr, "usage: labeleval project issueconfig\n")
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
	issue        *github.Issue
}

// A reponse holds the return values from [labels.IssueCategory].
type response struct {
	cat         labels.Category
	explanation string
	err         error
}

func run(ctx context.Context, project, issueConfigFile string) error {
	if *count <= 0 {
		return fmt.Errorf("bad count: %d", *count)
	}

	cats := labels.CategoriesForProject(project)
	if cats == nil {
		return fmt.Errorf("unknown project: %s", project)
	}
	knownCategories := map[string]bool{}
	for _, c := range cats {
		knownCategories[c.Name] = true
	}

	allIssueConfigs, err := readIssueFile(issueConfigFile)
	if err != nil {
		return err
	}
	var issueConfigs []*issueConfig
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
	if len(issueConfigs) == 0 {
		return errors.New("no issues to evaluate")
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

	start := time.Now()
	if err := lookupIssues(db, issueConfigs); err != nil {
		return err
	}
	log.Printf("looked up %d issues in %.1fs", len(issueConfigs), time.Since(start).Seconds())

	responseLists := make([][]response, len(issueConfigs))
	for i := range responseLists {
		responseLists[i] = make([]response, *count)
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(10)
	start = time.Now()
	for c := range *count {
		for i, ic := range issueConfigs {
			g.Go(func() error {
				got, exp, err := labels.IssueCategory(gctx, db, cgen, ic.issue)
				responseLists[i][c] = response{got, exp, err}
				return nil
			})
		}
	}
	if err := g.Wait(); err != nil {
		return err
	}
	log.Printf("evaluated %d times with %s in %.1f seconds",
		*count, cgen.Model(), time.Since(start).Seconds())

	printResults(issueConfigs, responseLists, knownCategories)
	return nil
}

func lookupIssues(db storage.DB, ics []*issueConfig) error {
	for _, ic := range ics {
		issue, err := github.LookupIssue(db, "golang/go", int64(ic.Number))
		if err != nil {
			return err
		}
		ic.issue = issue
	}
	return nil
}
func printResults(issueConfigs []*issueConfig, responseLists [][]response, knownCategories map[string]bool) {
	passes := 0
Issues:
	for i, ic := range issueConfigs {
		responseList := responseLists[i]
		fmt.Printf("%-6d ", ic.Number)
		if len(responseList) == 1 {
			res := responseList[0]
			if res.err != nil {
				fmt.Printf("ERROR %v\n", res.err)
			} else if !knownCategories[ic.WantCategory] {
				fmt.Printf("NEW got %s; want %s is not in list of known categories\n",
					res.cat.Name, ic.WantCategory)
			} else if res.cat.Name != ic.WantCategory {
				fmt.Printf("FAIL got %s want %s\n", res.cat.Name, ic.WantCategory)
				fmt.Printf("%11s\n", res.explanation)
			} else {
				fmt.Printf("PASS\n")
				passes++
			}
		} else {
			// If any response is an error or NEW, stop there.
			for _, res := range responseList {
				if res.err != nil {
					fmt.Printf("ERROR %v\n", res.err)
					continue Issues
				}
				if !knownCategories[ic.WantCategory] {
					fmt.Printf("NEW got %s; want %s is not in list of known categories\n",
						res.cat.Name, ic.WantCategory)
					continue Issues
				}
			}
			// Group by category.
			counts := map[string]int{}
			for _, res := range responseList {
				counts[res.cat.Name]++
			}
			p := counts[ic.WantCategory]
			f := len(responseList) - p
			fmt.Printf("PASS:%d FAIL:%d ", p, f)
			if p > f {
				passes++
			}
			// Sort passes first, then alphabetically.
			cats := slices.Collect(maps.Keys(counts))
			slices.SortFunc(cats, func(c1, c2 string) int {
				if c1 == c2 {
					return 0
				}
				if c1 == ic.WantCategory {
					return -1
				}
				if c2 == ic.WantCategory {
					return 1
				}
				return strings.Compare(c1, c2)
			})
			for _, c := range cats {
				fmt.Printf("  %s:%d", c, counts[c])
			}
			fmt.Println()
		}
	}
	total := len(issueConfigs)
	fmt.Printf("%d passed/%d total = %.1f%%\n", passes, total, float64(passes*100)/float64(total))
}

func newGeminiClient(ctx context.Context, lg *slog.Logger) (*gemini.Client, error) {
	sdb := secret.Netrc()
	c, err := gemini.NewClient(ctx, lg, sdb, http.DefaultClient,
		gemini.DefaultEmbeddingModel, gemini.DefaultGenerativeModel)
	if err != nil {
		return nil, err
	}
	c.SetTemperature(0)
	return c, nil
}

func readYAMLFile(filename string, p any) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	return dec.Decode(p)
}

func readIssueFile(filename string) ([]*issueConfig, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var issues []*issueConfig
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
		issues = append(issues, &issueConfig{Number: n, WantCategory: strings.TrimSpace(rest)})
	}
	return issues, nil
}

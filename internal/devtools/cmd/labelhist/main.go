// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Labelhist displays the events of a GitHub issue that affect its labels.

Usage:

	labelhist issues...

Each argument can be a single issue number or a range of numbers "from-to".
*/
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"golang.org/x/oscar/internal/gcp/firestore"
	"golang.org/x/oscar/internal/github"
)

var project = flag.String("project", "golang/go", "GitHub project")

func usage() {
	fmt.Fprintf(os.Stderr, "usage: labelhist issues...\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("labelhist: ")
	flag.Usage = usage
	flag.Parse()
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	var ranges []Range
	for _, arg := range flag.Args() {
		r, err := parseIssueArg(arg)
		if err != nil {
			return err
		}
		ranges = append(ranges, r)
	}
	lg := slog.New(slog.NewTextHandler(os.Stderr, nil))
	db, err := firestore.NewDB(ctx, lg, "oscar-go-1", "prod")
	if err != nil {
		return err
	}

	for _, r := range ranges {
		for ev := range github.Events(db, *project, r.min, r.max) {
			switch ev.API {
			case "/issues":
				fmt.Printf("%d:\n", ev.Issue)
			case "/issues/events":
				ie := ev.Typed.(*github.IssueEvent)
				if ie.Event == "labeled" || ie.Event == "unlabeled" {
					c := '+'
					if ie.Event == "unlabeled" {
						c = '-'
					}
					fmt.Printf("  %s %-10s %c%s\n", ie.CreatedAt, ie.Actor.Login, c, ie.Label.Name)
				}
			}
		}
	}
	return nil
}

type Range struct {
	min, max int64
}

func parseIssueArg(s string) (Range, error) {
	sfrom, sto, found := strings.Cut(s, "-")
	from, err := strconv.ParseInt(sfrom, 10, 64)
	if err != nil {
		return Range{}, err
	}
	if !found {
		return Range{from, from}, nil
	}
	to, err := strconv.ParseInt(sto, 10, 64)
	if err != nil {
		return Range{}, err
	}
	return Range{from, to}, nil
}

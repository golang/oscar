// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
	 actionlog works with the actionlog.
	 It can be used to approve or deny actions in bulk, as an alternative to the UI.

	 Usage:

		go run . DBSPEC list FROM TO
		List entries between times.
		FROM and TO are times expressed in one of the following ways:
		- times in DateOnly or RFC3339 format
		- negative durations in a format understood by time.ParseDuration,
		  meaning time.Now().Add(d). For example, "-2h" for "2 hours ago".
		- the word "now"

		go run . DBSPEC get KEY...
		Display entries with given keys.

		go run . DBSPEC [approve|deny] KEY...
		Approve or deny entries with given keys.

	 The DBSPEC argument is a db spec like firestore:PROJECT,DBNAME

	 The key format for the -prefix option and KEY arguments is a comma-separated list
	 of strings or ints, processed with ordered.Encode. For example,

		golang/go,27

	 is equivalent to the Go expression

		ordered.Encode("golang/go", 27)

Examples:

Deny labeling all golang/go issues from 1 to 100:

	seq 1 100 | xargs go run . -kind labels.Labeler -prefix golang/go firestore:oscar-go-1,prod deny
*/
package main

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oscar/internal/actions"
	"golang.org/x/oscar/internal/dbspec"
	"golang.org/x/oscar/internal/storage"
	"rsc.io/ordered"
)

var flags struct {
	kind      string
	keyPrefix string
}

func init() {
	flag.StringVar(&flags.kind, "kind", "", "action kind")
	flag.StringVar(&flags.keyPrefix, "prefix", "", "prefix for keys")
}

var logger = slog.Default()

func usage() {
	fmt.Fprintf(os.Stderr, "usage: actionlog [OPTIONS] dbspec subcommand\n")
	fmt.Fprintf(os.Stderr, "subcommands are list, get, deny, approve\n")
	fmt.Fprintf(os.Stderr, "see package doc for details\n")

	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("actionlog: ")
	flag.Usage = usage
	flag.Parse()
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()
	args := flag.Args()
	if len(args) < 2 {
		usage()
	}
	spec, err := dbspec.Parse(flag.Arg(0))
	if err != nil {
		return err
	}
	db, err := spec.Open(ctx, logger)
	if err != nil {
		return err
	}

	switch args[1] {
	case "list":
		return doList(db, args[2:])
	case "get":
		return doGet(db, args[2:])
	case "approve":
		return doDecide(db, true, args[2:])
	case "deny":
		return doDecide(db, false, args[2:])
	default:
		usage()
	}
	return nil
}

func doList(db storage.DB, args []string) error {
	if len(args) != 2 {
		return errors.New("usage: list FROM TO")
	}
	from, err1 := parseTimeArg(args[0])
	to, err2 := parseTimeArg(args[1])
	if err := cmp.Or(err1, err2); err != nil {
		return err
	}
	filter := func(kind string, key []byte) bool {
		if flags.kind != "" && kind != flags.kind {
			return false
		}
		if flags.keyPrefix != "" {
			prefix := parseOrdered(flags.keyPrefix)
			if prefix != nil && !bytes.HasPrefix(key, prefix) {
				return false
			}
		}
		return true
	}
	fmt.Printf("listing action log entries from %s to %s\n", from.Format(time.DateTime), to.Format(time.DateTime))
	for entry := range actions.ScanAfter(logger, db, from.Add(-1), filter) {
		if entry.Created.After(to) {
			break
		}
		showEntry(entry)
	}
	return nil
}

func parseTimeArg(s string) (time.Time, error) {
	if s == "now" {
		return time.Now(), nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().Add(d), nil
	}
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, errors.New("could not parse time or interval")
}

func showEntry(e *actions.Entry) {
	fmt.Printf("%s %s\n", e.Kind, e.Created.Format(time.DateTime))
	fmt.Printf("\tkey: %s\n", storage.Fmt(e.Key))
	fmt.Printf("\tapproval: ")
	if !e.ApprovalRequired {
		fmt.Printf(" not required\n")
	} else if e.Approved() {
		fmt.Println("approved")
	} else if len(e.Decisions) > 0 {
		fmt.Println("denied")
	} else {
		fmt.Println("required")
	}
	fmt.Printf("\tdone: ")
	if e.Done.IsZero() {
		fmt.Printf("-\n")
	} else {
		fmt.Printf("%s\n", e.Done.Format(time.DateTime))
	}
	fmt.Printf("\t%s\n", e.ActionForDisplay())
	fmt.Println()

}
func doGet(db storage.DB, args []string) error {
	if flags.kind == "" {
		return errors.New("need -kind")
	}
	for _, skey := range args {
		key := buildKey(skey)
		if key == nil {
			return fmt.Errorf("bad key: %q", skey)
		}
		e, ok := actions.Get(db, flags.kind, key)
		if !ok {
			return fmt.Errorf("key %q: no action", skey)
		}
		showEntry(e)
	}
	return nil
}

func buildKey(s string) []byte {
	if flags.keyPrefix != "" {
		s = flags.keyPrefix + "," + s
	}
	return parseOrdered(s)
}

func parseOrdered(s string) []byte {
	var parts []any
	words := strings.Split(s, ",")
	if len(words) == 1 && strings.TrimSpace(words[0]) == "" {
		return nil
	}
	for _, p := range words {
		var part any
		p = strings.TrimSpace(p)
		if i, err := strconv.Atoi(p); err == nil {
			part = i
		} else {
			part = p
		}
		parts = append(parts, part)
	}
	return ordered.Encode(parts...)
}

func doDecide(db storage.DB, approve bool, args []string) error {
	if flags.kind == "" {
		return errors.New("need -kind")
	}
	dec := actions.Decision{
		Name:     os.Getenv("USER"),
		Approved: approve,
	}
	for _, skey := range args {
		key := buildKey(skey)
		if key == nil {
			return fmt.Errorf("bad key: %q", skey)
		}
		e, ok := actions.Get(db, flags.kind, key)
		if !ok {
			fmt.Printf("%s: no action\n", skey)
			continue
		}
		if !e.ApprovalRequired {
			fmt.Printf("%s: approval not required\n", skey)
			continue
		}
		if e.Approved() {
			fmt.Printf("%s: already approved\n", skey)
			continue
		}
		if len(e.Decisions) > 0 {
			fmt.Printf("%s: already denied\n", skey)
			continue
		}
		dec.Time = time.Now()
		actions.AddDecision(db, flags.kind, key, dec)
		fmt.Printf("%s: ", skey)
		if approve {
			fmt.Println("approved")
		} else {
			fmt.Println("denied")
		}
	}
	return nil
}

// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

// Show shows the result of running htmlutil.Split on a single input file.
//
// Usage:
//
//	go run show.go file.html
//
// It prints lines that can be pasted into the test table in ../split_test.go.
package main

import (
	"fmt"
	"log"
	"os"

	"golang.org/x/oscar/internal/htmlutil"
)

func main() {
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}

	for s := range htmlutil.Split(data) {
		fmt.Printf("{%q, %q, %q},\n", s.Title, s.ID, s.Text[:min(len(s.Text), 40)])
	}
}

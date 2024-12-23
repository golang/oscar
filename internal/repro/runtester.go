// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

// runtester is used by LocalTester to run a test.
// This is run in a directory with the go repo checked out.
// It is invoked with the path of the program to run;
// that program should start with a comment showing how it should be run.
package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: runtester PROGRAM")
		os.Exit(1)
	}
	if !filepath.IsAbs(os.Args[1]) {
		fmt.Fprintf(os.Stderr, "runtester: not absolute path: %s\n", os.Args[1])
	}

	wd, err := os.Getwd()
	if err != nil {
		log.Fatalf("can't get working directory: %v", err)
	}

	// Start by building the Go repo at the current version.
	// We assume a sufficiently new go program is on PATH.

	var run string
	if runtime.GOOS == "windows" {
		run = "./make.bat"
	} else {
		run = "./make.bash"
	}
	cmd := exec.Command(run)
	cmd.Dir = filepath.Join(wd, "src")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("building Go repo failed: %v\n", err)
	}

	// Read the first line of the test file.

	testFile := os.Args[1]
	testBody, err := os.ReadFile(testFile)
	if err != nil {
		log.Fatal(err)
	}
	line, _, _ := bytes.Cut(testBody, []byte{'\n'})

	// The first line should be a Go comment
	// that includes the command to run.
	// This will be something like "go run" or "go test".

	line = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("//")))
	args := strings.Fields(string(line))
	if args[0] != "go" {
		log.Fatal(`command to run is not "go"`)
	}
	args[0] = filepath.Join(wd, "bin/go")

	// Append the name of the test file to the command.

	testDir, testFile := filepath.Split(testFile)
	args = append(args, testFile)

	// Run the test.

	cmd = exec.Command(args[0], args[1:]...)
	cmd.Dir = testDir
	if err := cmd.Run(); err != nil {
		log.Fatal(err)
	}
	os.Exit(0)
}

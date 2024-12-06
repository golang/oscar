// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

package main

import (
	"log"
	"os"
	"os/exec"
)

// runner is the entry point to the gVisor sandbox and it
// executes the bisection regression test.
//
// Currently, it expects that regression is a "regression_test.go"
// file in the same directory. It also expects that a repository
// is a Go repo, built and located in "go-bisect" directory next
// to the runner.

func main() {
	cmd := exec.Command("./go-bisect/bin/go", "test", "regression_test.go")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Fatalf("runner start error: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		if exiterr, ok := err.(*exec.ExitError); ok {
			os.Exit(exiterr.ExitCode())
		} else {
			log.Fatalf("cmd.Wait: %v", err)
		}
	}
}

// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package sandbox runs programs in a secure gvisor environment.
package sandbox

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// A Sandbox is a restricted execution environment.
// A Sandbox instance refers to a directory containing an OCI
// bundle (see https://github.com/opencontainers/runtime-spec/blob/main/bundle.md).
type Sandbox struct {
	bundleDir string
	Runsc     string // path to runsc program
}

// New returns a new gvisor Sandbox using the bundle in bundleDir.
// The bundle must be configured to run the 'runner' program,
// built from runner.go in this directory.
// The Sandbox expects the runsc program to be on the path.
// That can be overridden by setting the Runsc field.
func New(bundleDir string) *Sandbox {
	return &Sandbox{
		bundleDir: bundleDir,
		Runsc:     "runsc",
	}
}

// Cmd's exported fields must be a subset of the exported fields of exec.Cmd.
// runner.go must be able to unmarshal a sandbox.Cmd into an exec.Cmd.

// Cmd describes how to run a binary in a sandbox.
type Cmd struct {
	sb *Sandbox

	// Path is the path of the command to run.
	//
	// This is the only field that must be set to a non-zero
	// value. If Path is relative, it is evaluated relative
	// to Dir.
	Path string

	// Args holds command line arguments, including the command as Args[0].
	// If the Args field is empty or nil, Run uses {Path}.
	//
	// In typical use, both Path and Args are set by calling Command.
	Args []string

	// Env specifies the environment of the process.
	// Each entry is of the form "key=value".
	// If Env is nil, the new process uses whatever environment
	// runsc provides by default.
	Env []string

	// If AppendToEnv is true, the contents of Env are appended
	// to the sandbox's existing environment, instead of replacing it.
	AppendToEnv bool

	// Dir specifies the working directory of the command.
	// If Dir is the empty string, Run runs the command in the
	// root of the sandbox filesystem.
	Dir string
}

// Command creates a *Cmd to run path in the sandbox.
// It behaves like [os/exec.Command].
func (s *Sandbox) Command(path string, arg ...string) *Cmd {
	return &Cmd{
		sb:   s,
		Path: path,
		Args: append([]string{path}, arg...),
	}
}

// Output runs Cmd in the sandbox used to create it, and returns its standard output.
func (c *Cmd) Output() ([]byte, error) {
	if err := c.sb.Validate(); err != nil {
		return nil, err
	}
	// -ignore-cgroups is needed to avoid this error from runsc:
	// cannot set up cgroup for root: configuring cgroup: write /sys/fs/cgroup/cgroup.subtree_control: device or resource busy
	sid := newSandboxID(time.Now().String(), c) // create a unique sandbox id
	cmd := exec.Command(c.sb.Runsc, "-ignore-cgroups", "-network=none", "-platform=systrap", "run", "sandbox"+sid)
	cmd.Dir = c.sb.bundleDir

	// Invoking runsc will result in the invocation
	// of the runner.go program (in this directory)
	// in a sandbox. The runner then waits for the
	// marshalled c as input. Once it receives the
	// command, the runner executes it.
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdin, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	ch := make(chan error, 1)
	go func() {
		_, err := stdinPipe.Write(stdin)
		stdinPipe.Close()
		ch <- err
	}()

	// Once c is finished executing in the sandbox,
	// via runner, we collect and return its output.
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	if err := <-ch; err != nil {
		return nil, fmt.Errorf("writing stdin: %w", err)
	}
	return bytes.TrimSpace(out), nil
}

// newSandboxID creates a unique hex ID for c
// based on the command data and a seed.
func newSandboxID(seed string, c *Cmd) string {
	hasher := sha256.New()
	io.WriteString(hasher, seed)
	io.WriteString(hasher, c.Path)
	io.WriteString(hasher, c.Dir)
	for _, a := range c.Args {
		io.WriteString(hasher, a)
	}
	for _, e := range c.Env {
		io.WriteString(hasher, e)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// ociConfig is a subset of the OCI container configuration.
// It is used by Validate to unmarshal the bundle's config.json.
type ociConfig struct {
	Version string  `json:"ociVersion"`
	Mounts  []mount `json:"mounts"`
}

// mount represents an OCI mount.
type mount struct {
	Destination string   `json:"destination"`
	Type        string   `json:"type"`
	Source      string   `json:"source"`
	Options     []string `json:"options"`
}

// Validate the sandbox configuration.
func (s *Sandbox) Validate() error {
	f, err := os.Open(filepath.Join(s.bundleDir, "config.json"))
	if err != nil {
		return err
	}
	defer f.Close()
	var config ociConfig
	if err := json.NewDecoder(f).Decode(&config); err != nil {
		return err
	}
	const wantVersion = "1.0.0"
	if config.Version != wantVersion {
		return fmt.Errorf("ociVersion: got %q, want %q", config.Version, wantVersion)
	}
	for _, m := range config.Mounts {
		if isBindMount(m) {
			_, err := os.Stat(m.Source)
			if err != nil {
				return fmt.Errorf("bind mount source: %w", err)
			}
		}
	}
	return nil
}

func isBindMount(m mount) bool {
	for _, opt := range m.Options {
		if opt == "bind" {
			return true
		}
	}
	return false
}

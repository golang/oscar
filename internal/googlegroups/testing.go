// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package googlegroups

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/txtar"
)

// divertConversations reports whether conversations
// are being diverted for testing purposes.
func (c *Client) divertChanges() bool {
	return c.testing && c.testClient != nil
}

// Testing returns a TestingClient, which provides access to Client functionality
// intended for testing.
// Testing only returns a non-nil TestingClient in testing mode,
// which is active if the current program is a test binary (that is, [testing.Testing] returns true).
// Otherwise, Testing returns nil.
//
// Each Client has only one TestingClient associated with it. Every call to Testing returns the same TestingClient.
func (c *Client) Testing() *TestingClient {
	if !testing.Testing() && !c.testing {
		return nil
	}

	c.testMu.Lock()
	defer c.testMu.Unlock()
	if c.testClient == nil {
		c.testClient = newTestingClient(c)
	}
	return c.testClient
}

func newTestingClient(c *Client) *TestingClient {
	return &TestingClient{c: c, interrupted: make(map[string]bool)}
}

// A TestingClient provides access to [Client] functionality intended for testing.
type TestingClient struct {
	c           *Client
	convs       []*Conversation // conversation updates, in reverse chronological order
	searchLimit int             // mimic Google Groups search limits
	// interrupted keeps track for which conversations
	// we already injected interruption. This is needed
	// since Google Groups time stamps are not fine grained
	// enough for the next invocation of [Client.Sync] just
	// to continue from the interrupted conversation.
	interrupted map[string]bool
}

func (tc *TestingClient) limit() int {
	tc.c.testMu.Lock()
	defer tc.c.testMu.Unlock()
	return tc.searchLimit
}

func (tc *TestingClient) setLimit(l int) {
	tc.c.testMu.Lock()
	defer tc.c.testMu.Unlock()
	tc.searchLimit = l
}

// LoadTxtar loads a conversation info history from the named txtar file,
// and adds it to tc.convs.
//
// The file should contain a txtar archive (see [golang.org/x/tools/txtar]).
// Each file in the archive may be named “conversation #n” (for example
// “conversation#1”).
// A line in the file must be in the format "key: value", where "key" is one
// of the fields of [Conversation] type.
func (tc *TestingClient) LoadTxtar(file string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	err = tc.LoadTxtarData(data)
	if err != nil {
		err = &os.PathError{Op: "load", Path: file, Err: err}
	}
	return err
}

// LoadTxtarData loads a change info history from the txtar file content data.
// See [LoadTxtar] for a description of the format.
func (tc *TestingClient) LoadTxtarData(data []byte) error {
	ar := txtar.Parse(data)
	for _, file := range ar.Files {
		data := string(file.Data)
		// Skip the name and proceed to read headers.
		c := &Conversation{}
		for {
			line, rest, _ := strings.Cut(data, "\n")
			data = rest
			if line == "" {
				break
			}
			key, val, ok := strings.Cut(line, ":")
			if !ok {
				return fmt.Errorf("%s: invalid header line: %q", file.Name, line)
			}
			val = strings.TrimSpace(val)
			if val == "" {
				continue
			}
			switch key {
			case "Group":
				c.Group = val
			case "URL":
				c.URL = val
			case "HTML":
				c.HTML = []byte(val)
			case "Updated":
				c.updated = val
			case "interrupt":
				b, err := strconv.ParseBool(val)
				if err != nil {
					return err
				}
				c.interrupt = b
			}
		}
		tc.c.testMu.Lock()
		tc.convs = append(tc.convs, c)
		tc.c.testMu.Unlock()
	}
	return nil
}

func (tc *TestingClient) conversations(_ context.Context, group, after, before string) iter.Seq2[*Conversation, error] {
	return func(yield func(*Conversation, error) bool) {
		inInterval := false
		yielded := 0 // yielded in a single batch
		for _, c := range tc.convs {
			in, err := updatedIn(c, after, before)
			if err != nil {
				yield(nil, err)
				return
			}
			if !in {
				if inInterval { // reached outside of the interval
					return
				}
				continue
			}

			// We are inside the matching interval.
			inInterval = true

			if c.Group != group {
				continue
			}

			yielded++
			if !yield(c, nil) {
				return
			}

			// Fake an interruption if the same interruption
			// was not injected earlier.
			if c.interrupt && !tc.interrupted[c.URL] {
				tc.interrupted[c.URL] = true
				yield(nil, errors.New("test interrupt error"))
				return
			}

			if yielded >= tc.limit() { // reached the search limit
				return
			}
		}
	}
}

// updatedIn reports if c was updated in the (after, before) interval.
// Both after and before must be in timeStampLayout.
func updatedIn(c *Conversation, after, before string) (bool, error) {
	u, err := time.Parse(timeStampLayout, c.updated)
	if err != nil {
		return false, err
	}

	ain := true
	if after != "" {
		a, err := time.Parse(timeStampLayout, after)
		if err != nil {
			return false, err
		}
		ain = a.Before(u)
	}
	bin := true
	if before != "" {
		b, err := time.Parse(timeStampLayout, before)
		if err != nil {
			return false, err
		}
		bin = b.After(u)
	}
	return ain && bin, nil
}

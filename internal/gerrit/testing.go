// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gerrit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/oscar/internal/storage"
	"golang.org/x/tools/txtar"
)

// divertChanges reports whether changes and their
// comments are being diverted for testing purposes.
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

	if c.testClient == nil {
		c.testClient = &TestingClient{}
	}
	return c.testClient
}

// A TestingClient provides access to [Client] functionality intended for testing.
type TestingClient struct {
	chs        []*ChangeInfo // change updates, in reverse chronological order
	queryLimit int           // mimic Gerrit query limits
}

// LoadTxtar loads a change info history from the named txtar file,
// and adds it to tc.chs.
//
// The file should contain a txtar archive (see [golang.org/x/tools/txtar]).
// Each file in the archive may be named “change#n” (for example “change#1”).
// A line in the file must be in the format "key: value", where "key" is one
// of the fields of [ChangeInfo] type.
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
		c := &ChangeInfo{}
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
			case "Number":
				i, err := strconv.Atoi(val)
				if err != nil {
					return err
				}
				c.Number = i
			case "Updated":
				t, err := timestamp(val)
				if err != nil {
					return err
				}
				c.Updated = t
			case "MetaRevID":
				c.MetaRevID = val
			case "interrupt":
				b, err := strconv.ParseBool(val)
				if err != nil {
					return err
				}
				c.interrupt = b
			}
		}
		tc.chs = append(tc.chs, c)
	}
	return nil
}

// changes returns an iterator of change updates in tc.chs that are updated
// in the interval [after, before], in reverse chronological order. First
// skip number of matching change updates are disregarded.
func (tc *TestingClient) changes(_ context.Context, _ string, after, before string, skip int) iter.Seq2[json.RawMessage, error] {
	return func(yield func(json.RawMessage, error) bool) {
		skipped := 0
		inInterval := false
		yielded := 0 // yielded in a single batch
		for _, c := range tc.chs {
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
			if skip > 0 && skipped < skip {
				skipped++
				continue
			}

			cj, err := json.Marshal(c)
			if err != nil {
				yield(nil, err)
				return
			}

			yielded++
			if !yield(cj, nil) {
				return
			}

			if c.interrupt { // fake an interruption
				yield(nil, errors.New("test interrupt error"))
				return
			}

			if yielded >= tc.queryLimit { // reached the batch limit
				return
			}
		}
	}
}

// updatedIn reports if c was updated in the [after, before] interval.
// Both after and before must be in gerrit timestamp layout.
func updatedIn(c *ChangeInfo, after, before string) (bool, error) {
	u := c.Updated.Time()

	ain := true
	if after != "" {
		a, err := timestamp(after)
		if err != nil {
			return false, err
		}
		ain = a.Time().Equal(u) || a.Time().Before(u)
	}
	bin := true
	if before != "" {
		b, err := timestamp(before)
		if err != nil {
			return false, err
		}
		bin = b.Time().Equal(u) || b.Time().After(u)
	}
	return ain && bin, nil
}

func timestamp(gt string) (TimeStamp, error) {
	var ts TimeStamp
	if err := ts.UnmarshalJSON([]byte(quote(gt))); err != nil {
		return TimeStamp(time.Time{}), err
	}
	return ts, nil
}

// syncComments does nothing, for testing purposes.
func (tc *TestingClient) syncComments(_ context.Context, _ storage.Batch, _ string, _ int) error {
	return nil
}

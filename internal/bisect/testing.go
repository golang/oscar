// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bisect

import "testing"

// divertBisect reports whether bisection and its
// output are being diverted for testing purposes.
func (c *Client) divertBisect() bool {
	return testing.Testing() && c.testClient != nil
}

// Testing returns a TestingClient, which provides access to Client functionality
// intended for testing.
// Testing only returns a non-nil TestingClient in testing mode,
// which is active if the current program is a test binary (that is, [testing.Testing] returns true).
// Otherwise, Testing returns nil.
//
// Each Client has only one TestingClient associated with it. Every call to Testing returns the same TestingClient.
func (c *Client) Testing() *TestingClient {
	if !testing.Testing() {
		return nil
	}

	c.testMu.Lock()
	defer c.testMu.Unlock()
	if c.testClient == nil {
		c.testClient = &TestingClient{}
	}
	return c.testClient
}

// A TestingClient provides access to [Client] functionality intended for testing.
type TestingClient struct {
	Output string
}

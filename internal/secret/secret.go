// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package secret defines an interface to a database storing secrets, such as passwords and API keys.
//
// TODO(rsc): Consider adding a basic key: value text file format besides netrc.
package secret

import (
	"os"
	"path/filepath"
	"strings"
)

// A DB is a secret database, which is a persistent map from names to secret values.
type DB interface {
	Get(name string) (secret string, ok bool)
	Set(name, secret string)
}

// Empty returns a read-only, empty secret database.
func Empty() DB {
	return ReadOnlyMap(nil)
}

// A Map is a read-write, in-memory [DB].
type Map map[string]string

// Get returns the named secret.
func (m Map) Get(name string) (secret string, ok bool) {
	secret, ok = m[name]
	return
}

// Set adds a secret with the given name.
func (m Map) Set(name, secret string) {
	m[name] = secret
}

// A ReadOnlyMap is a read-only [DB]. Calling [Set] panics.
type ReadOnlyMap map[string]string

// Get returns the named secret.
func (m ReadOnlyMap) Get(name string) (secret string, ok bool) {
	secret, ok = m[name]
	return
}

// Set panics.
func (m ReadOnlyMap) Set(name, secret string) {
	panic("read-only secrets")
}

// Netrc returns a read-only secret database initialized by the content of $HOME/.netrc, if it exists.
// A line in .netrc of the form
//
//	machine name login user password pass
//
// causes Get("name") to return "user:pass".
// Lines later in .netrc take priority over lines earlier in .netrc.
//
// If the environment $NETRC is set and non-empty, the file it names is used
// instead of $HOME/.netrc.
func Netrc() ReadOnlyMap {
	file := os.Getenv("NETRC")
	if file == "" {
		dir, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		file = filepath.Join(dir, ".netrc")
	}
	return openNetrc(file)
}

func openNetrc(file string) ReadOnlyMap {
	m := make(ReadOnlyMap)
	if data, err := os.ReadFile(file); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			if len(f) == 6 && f[0] == "machine" && f[2] == "login" && f[4] == "password" {
				m[f[1]] = f[3] + ":" + f[5]
			}
		}
	}
	return m
}

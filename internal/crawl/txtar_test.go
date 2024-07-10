// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package crawl

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"golang.org/x/tools/txtar"
)

type testTransport struct {
	file string
	m    map[string][]byte
}

func readTestClient(t *testing.T, file string) *http.Client {
	ar, err := txtar.ParseFile(file)
	if err != nil {
		t.Fatal(err)
	}
	m := make(map[string][]byte)
	for _, f := range ar.Files {
		if !strings.HasPrefix(f.Name, "http://") && !strings.HasPrefix(f.Name, "https://") {
			t.Fatalf("%s: bad file name %s", file, f.Name)
		}
		m[f.Name] = f.Data
	}
	return &http.Client{Transport: &testTransport{file, m}}
}

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u1 := *req.URL
	if u1.Host == "" {
		u1.Host = req.Host
	}
	u := u1.String()
	data := t.m[u]
	if data == nil {
		return nil, fmt.Errorf("no response in %s for %s", t.file, u)
	}
	if bytes.HasPrefix(data, []byte("panic")) {
		panic("should not fetch " + u)
	}
	if bytes.HasPrefix(data, []byte("bigbody")) {
		data = bigbody
	}
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(data)), req)
	if strings.Contains(u, "/err/body-read-error") {
		resp.Body = io.NopCloser(iotest.ErrReader(errors.New("body read error!")))
	}
	return resp, err
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

var bigbody = []byte(`HTTP/1.1 200 OK
Content-Type: text.html

` + strings.Repeat("This is far too much data.\n", 1<<20))

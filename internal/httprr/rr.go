// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package httprr implements HTTP record and replay, mainly for use in tests.
//
// [Open] creates a new [RecordReplay]. Whether it is recording or replaying
// is controlled by the -httprecord flag, which is defined by this package
// only in test programs (built by “go test”).
// See the [Open] documentation for more details.
package httprr

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

var record = new(string)

func init() {
	if testing.Testing() {
		record = flag.String("httprecord", "", "re-record traces for files matching `regexp`")
	}
}

// A RecordReplay is an [http.RoundTripper] that can operate in two modes: record and replay.
//
// In record mode, the RecordReplay invokes another RoundTripper
// and logs the (request, response) pairs to a file.
//
// In replay mode, the RecordReplay responds to requests by finding
// an identical request in the log and sending the logged response.
type RecordReplay struct {
	file string
	real http.RoundTripper

	mu     sync.Mutex
	broken error
	record *os.File
	replay map[string]string
	scrub  []func(*http.Request) error
}

// Scrub adds new scrubbing functions to rr.
//
// Before using a request as a lookup key or saving it in the record/replay log,
// the RecordReplay calls each scrub function, in the order they were registered,
// to canonicalize non-deterministic parts of the request and remove secrets.
// Scrubbing only applies to a copy of the request used in the record/replay log;
// the unmodified original request is sent to the actual server in recording mode.
// A scrub function can assume that if req.Body is not nil, then it has type [*Body].
//
// Calling Scrub adds to the list of registered scrubbing functions;
// it does not replace those registered by earlier calls.
func (rr *RecordReplay) Scrub(scrubs ...func(req *http.Request) error) {
	rr.scrub = append(rr.scrub, scrubs...)
}

// Recording reports whether the rr is in recording mode.
func (rr *RecordReplay) Recording() bool {
	return rr.record != nil
}

// Open opens a new record/replay log in the named file and
// returns a [RecordReplay] backed by that file.
//
// By default Open expects the file to exist and contain a
// previously-recorded log of (request, response) pairs,
// which [RecordReplay.RoundTrip] consults to prepare its responses.
//
// If the command-line flag -httprecord is set to a non-empty
// regular expression that matches file, then Open creates
// the file as a new log. In that mode, [RecordReplay.RoundTrip]
// makes actual HTTP requests using rt but then logs the requests and
// responses to the file for replaying in a future run.
func Open(file string, rt http.RoundTripper) (*RecordReplay, error) {
	if *record != "" {
		re, err := regexp.Compile(*record)
		if err != nil {
			return nil, fmt.Errorf("invalid -httprecord flag: %v", err)
		}
		if re.MatchString(file) {
			return create(file, rt)
		}
	}
	return open(file, rt)
}

// creates creates a new record-mode RecordReplay in the file.
// TODO maybe export
func create(file string, rt http.RoundTripper) (*RecordReplay, error) {
	f, err := os.Create(file)
	if err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(f, "httprr trace v1\n"); err != nil {
		// unreachable unless write error immediately after os.Create
		f.Close()
		return nil, err
	}
	rr := &RecordReplay{
		file:   file,
		real:   rt,
		record: f,
	}
	return rr, nil
}

// open opens a replay-mode RecordReplay using the data in the file.
func open(file string, rt http.RoundTripper) (*RecordReplay, error) {
	// Note: To handle larger traces without storing entirely in memory,
	// could instead read the file incrementally, storing a map[hash]offsets
	// and then reread the relevant part of the file during RoundTrip.

	bdata, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	data := string(bdata)
	line, data, ok := strings.Cut(data, "\n")
	if !ok || line != "httprr trace v1" {
		return nil, fmt.Errorf("read %s: not an httprr trace", file)
	}
	replay := make(map[string]string)
	for data != "" {
		line, data, ok = strings.Cut(data, "\n")
		f1, f2, _ := strings.Cut(line, " ")
		n1, err1 := strconv.Atoi(f1)
		n2, err2 := strconv.Atoi(f2)
		if !ok || err1 != nil || err2 != nil || n1 > len(data) || n2 > len(data[n1:]) {
			return nil, fmt.Errorf("read %s: corrupt httprr trace", file)
		}
		var req, resp string
		req, resp, data = data[:n1], data[n1:n1+n2], data[n1+n2:]
		replay[req] = resp
	}

	rr := &RecordReplay{
		file:   file,
		real:   rt,
		replay: replay,
	}
	return rr, nil
}

// Client returns an http.Client using rr as its transport.
// It is a shorthand for:
//
//	return &http.Client{Transport: rr}
//
// For more complicated uses, use rr or the [RecordReplay.RoundTrip] method directly.
func (rr *RecordReplay) Client() *http.Client {
	return &http.Client{Transport: rr}
}

// A Body is an io.ReadCloser used as an HTTP request body.
// In a Scrubber, if req.Body != nil, then req.Body is guaranteed
// to have type *Body, making it easy to access the body to change it.
type Body struct {
	Data       []byte
	ReadOffset int
}

// Read reads from the body, implementing io.Reader.
func (b *Body) Read(p []byte) (int, error) {
	n := copy(p, b.Data[b.ReadOffset:])
	if n == 0 {
		return 0, io.EOF
	}
	b.ReadOffset += n
	return n, nil
}

// Close is a no-op, implementing io.Closer.
func (b *Body) Close() error {
	return nil
}

// RoundTrip implements [http.RoundTripper].
//
// If rr has been opened in record mode, RoundTrip passes the requests on to
// the RoundTripper specified in the call to [Open] and then logs the
// (request, response) pair to the underlying file.
//
// If rr has been opened in replay mode, RoundTrip looks up the request in the log
// and then responds with the previously logged response.
// If the log does not contain req, RoundTrip returns an error.
func (rr *RecordReplay) RoundTrip(req *http.Request) (*http.Response, error) {
	// rkey is the scrubbed request used as a lookup key.
	rkey := req.Clone(context.Background())
	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, err
		}
		req.Body = &Body{Data: body}
		rkey.Body = &Body{Data: bytes.Clone(body)}
	}

	if len(rr.scrub) > 0 {
		// Canonicalize and scrub body.
		for _, scrub := range rr.scrub {
			if err := scrub(rkey); err != nil {
				return nil, err
			}
		}
	}

	// Now that scrubbers are done potentially modifying body, set length.
	if rkey.Body != nil {
		rkey.ContentLength = int64(len(rkey.Body.(*Body).Data))
	}

	// Use WriteProxy instead of Write to preserve the URL scheme.
	var bkey strings.Builder
	if err := rkey.WriteProxy(&bkey); err != nil {
		return nil, err
	}
	key := bkey.String()

	if rr.replay != nil {
		if respWire, ok := rr.replay[key]; ok {
			resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(respWire)), req)
			if err != nil {
				return nil, fmt.Errorf("read %s: corrupt httprr trace: %v", rr.file, err)
			}
			return resp, nil
		}
		return nil, fmt.Errorf("cached HTTP response not found for:\n%s", key)
	}

	rr.mu.Lock()
	err := rr.broken
	rr.mu.Unlock()
	if err != nil {
		return nil, err
	}

	resp, err := rr.real.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	var respBuf strings.Builder
	if err := resp.Write(&respBuf); err != nil {
		return nil, err
	}
	respWire := respBuf.String()

	resp, err = http.ReadResponse(bufio.NewReader(strings.NewReader(respWire)), req)
	if err != nil {
		// unreachable unless resp.Write does not round-trip with http.ReadResponse
		return nil, err
	}

	rr.mu.Lock()
	defer rr.mu.Unlock()
	if rr.broken != nil {
		// unreachable unless concurrent I/O error; checked above
		return nil, rr.broken
	}
	_, err1 := fmt.Fprintf(rr.record, "%d %d\n", len(key), len(respWire))
	_, err2 := rr.record.WriteString(key)
	_, err3 := rr.record.WriteString(respWire)
	if err := cmp.Or(err1, err2, err3); err != nil {
		rr.broken = err
		rr.record.Close()
		os.Remove(rr.file)
		return nil, err
	}
	return resp, nil
}

// Close closes the RecordReplay.
// It is a no-op in replay mode.
func (rr *RecordReplay) Close() error {
	if rr.broken != nil {
		return rr.broken
	}
	if rr.record != nil {
		return rr.record.Close()
	}
	return nil
}

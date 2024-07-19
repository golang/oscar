// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package grpcrr implements gRPC record and replay, mainly for use in tests.
// The client using gRPC must accept options of type
// [google.golang.org/api/option.ClientOption].
//
// [Open] creates a new [RecordReplay]. Whether it is recording or replaying
// is controlled by the -grpcrecord flag, which is defined by this package
// only in test programs (built by “go test”).
// See the [Open] documentation for more details.
package grpcrr

import (
	"flag"
	"fmt"
	"regexp"
	"testing"

	"github.com/google/go-replayers/grpcreplay"
	"google.golang.org/api/option"
)

var record = new(string)

func init() {
	if testing.Testing() {
		record = flag.String("grpcrecord", "", "re-record traces for files matching `regexp`")
	}
}

// A RecordReplay can operate in two modes: record and replay.
//
// In record mode, the RecordReplay intercepts gRPC calls
// and logs the requests and responses to a file.
//
// In replay mode, the RecordReplay responds to requests by finding an identical
// request in the log and sending the logged response.
type RecordReplay struct {
	recorder *grpcreplay.Recorder
	replayer *grpcreplay.Replayer
}

// Open opens a new record/replay log in the named file and
// returns a [RecordReplay] backed by that file.
//
// By default Open expects the file to exist and contain a
// previously-recorded log of RPCs, which are consulted for replies.
//
// If the command-line flag -grpcrecord is set to a non-empty regular expression
// that matches file, then Open creates the file as a new log.
// In that mode, actual RPCs are made and also logged to the file for replaying in
// a future run.
//
// After Open succeeds, pass the return value of [RecordReplay.ClientOptions] to
// a NewClient function to enable record/replay.
func Open(file string) (*RecordReplay, error) {
	if *record != "" {
		re, err := regexp.Compile(*record)
		if err != nil {
			return nil, fmt.Errorf("invalid -grpcrecord flag: %v", err)
		}
		if re.MatchString(file) {
			rec, err := grpcreplay.NewRecorder(file, &grpcreplay.RecorderOptions{Text: true})
			if err != nil {
				return nil, err
			}
			return &RecordReplay{recorder: rec}, nil
		}
	}
	rep, err := grpcreplay.NewReplayer(file, nil)
	if err != nil {
		return nil, err
	}
	return &RecordReplay{replayer: rep}, nil
}

// ClientOptions returns options to pass to a gRPC client
// that accepts them.
func (r *RecordReplay) ClientOptions() []option.ClientOption {
	if r.recorder != nil {
		var opts []option.ClientOption
		for _, gopt := range r.recorder.DialOptions() {
			opts = append(opts, option.WithGRPCDialOption(gopt))
		}
		return opts
	}
	conn, err := r.replayer.Connection()
	if err != nil {
		panic("replayer could not create connection")
	}
	return []option.ClientOption{option.WithGRPCConn(conn)}
}

// Close closes the RecordReplay.
func (rr *RecordReplay) Close() error {
	if rr.recorder != nil {
		return rr.recorder.Close()
	}
	return rr.replayer.Close()
}

// Recording reports whether rr is in recording mode.
func (rr *RecordReplay) Recording() bool {
	return rr.recorder != nil
}

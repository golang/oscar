// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package dbspec implements a string notation for referring to a database.
// A DB specification can take one of these forms:
//
// pebble:DIR[~VECTOR_NAMESPACE]
//
//	A Pebble database in the directory DIR.
//	DIR can be relative or absolute.
//
// firestore:PROJECT,DATABASE[~VECTOR_NAMESPACE]
//
//	A Firestore DB in the given GCP project and Firestore database.
//
// mem[~VECTOR_NAMESPACE]
//
//	An in-memory database.
//
// If a VECTOR_NAMESPACE is present, the spec refers to the vector DB portion of the database.
// The namespace can be empty, in which case the spec ends with a '~'.
package dbspec

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"golang.org/x/oscar/internal/gcp/firestore"
	"golang.org/x/oscar/internal/pebble"
	"golang.org/x/oscar/internal/storage"
)

// A Spec is the parsed representation of a DB specification string.
type Spec struct {
	Kind      string // "pebble", "firestore", etc.
	Location  string // directory, project, etc.
	Name      string // database name, for firestore
	IsVector  bool   // spec refers to the vector part of the database
	Namespace string // namespace of vector DB, possibly empty
}

func (s *Spec) String() string {
	var vs string
	if s.IsVector {
		vs = "~" + s.Namespace
	}
	switch s.Kind {
	case "mem":
		return "mem" + vs
	case "pebble":
		return "pebble:" + s.Location + vs
	case "firestore":
		return fmt.Sprintf("firestore:%s,%s%s", s.Location, s.Name, vs)
	default:
		return fmt.Sprintf("%#v", s)
	}
}

// Open opens the database described by the spec.
func (s *Spec) Open(ctx context.Context, lg *slog.Logger) (storage.DB, error) {
	switch s.Kind {
	case "mem":
		return storage.MemDB(), nil
	case "pebble":
		return pebble.Open(lg, s.Location)
	case "firestore":
		return firestore.NewDB(ctx, lg, s.Location, s.Name)
	default:
		return nil, fmt.Errorf("unknown DB kind %q", s.Kind)
	}
}

// Parse parses a DB specification string into a [Spec].
func Parse(s string) (_ *Spec, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("dbspec.Parse(%q): %v", s, err)
		}
	}()

	var kind, middle, ns string
	var hasTilde bool
	hasColon := strings.ContainsRune(s, ':')
	if hasColon {
		kind, middle, _ = strings.Cut(s, ":")
		middle, ns, hasTilde = strings.Cut(middle, "~")
	} else {
		kind, ns, hasTilde = strings.Cut(s, "~")
	}

	spec := &Spec{Kind: kind, IsVector: hasTilde, Namespace: ns}

	switch kind {
	case "mem":
		if hasColon {
			return nil, errors.New("invalid 'mem' spec: should be mem[~VECTOR_NAMESPACE]")
		}

	case "pebble":
		if len(middle) == 0 {
			return nil, errors.New("pebble spec missing directory; want pebble:DIR[~VECTOR_NAMESPACE]")
		}
		spec.Location = filepath.Clean(middle)

	case "firestore":
		proj, db, _ := strings.Cut(middle, ",")
		if proj == "" || db == "" {
			return nil, fmt.Errorf("invalid firestore spec; want firestore:PROJECT,DATABASE[~VECTOR_NAMESPACE]")
		}
		spec.Location = proj
		spec.Name = db

	default:
		return nil, fmt.Errorf("unknown kind %q", kind)
	}
	return spec, nil
}

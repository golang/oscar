// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package grpcerrors contains functions for working with
// errors from gRPC.
package grpcerrors

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// IsNotFound reports whether an error returned by a gRPC client is a NotFound
// error.
func IsNotFound(err error) bool {
	return status.Code(err) == codes.NotFound
}

// IsTimeout reports whether an error returned by a gRPC client indicates a timeout.
func IsTimeout(err error) bool {
	return status.Code(err) == codes.DeadlineExceeded
}

// IsUnavailable reports whether an error returned by a gRPC client indicates code “unavailable”.
func IsUnavailable(err error) bool {
	return status.Code(err) == codes.Unavailable
}

# Copyright 2024 The Go Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# This Dockerfile expects the build context to be the repo root.

################################################################
FROM golang:1.23rc2 AS builder

# Set the working directory outside $GOPATH to ensure module mode is enabled.
WORKDIR /src

# Copy go.mod and go.sum into the container.
# If they don't change, which is the common case, then docker can
# cache this COPY and the subsequent RUN.
COPY go.mod go.sum /

# Download the dependencies.
RUN go mod download

# Copy the repo from local machine into Docker client’s current working
# directory, so that we can use it to build the binary.
COPY . /src

# Build the binary.
RUN go build -mod=readonly ./internal/gaby

################################################################
# Use a smaller image to run the program.
# The resulting image is about 8x smaller than a golang image.
# Among other benefits, the space savings means more room for /tmp on Cloud Run.
FROM debian:stable-slim

LABEL maintainer="Go Oscar Team <oscar-team@google.com>"

# Copy CA certificates to prevent "x509: certificate signed by unknown authority" errors.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

WORKDIR app

COPY --from=builder src/gaby gaby

ARG FIRESTORE_DB
ENV OSCAR_FIRESTORE_DB=$FIRESTORE_DB

CMD ["./gaby"]

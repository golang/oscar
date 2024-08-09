# Copyright 2024 The Go Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# This Dockerfile expects the build context to be the repo root.
# It takes three build args:

#   FIRESTORE_DB: the name of the Firestore database to use
#   ENABLE_SYNC: whether syncing from GitHub is enabled (optional, default false)
#   ENABLE_CHANGES: whether writing to GitHub is enabled (optional, default false)
#
# Example:
#  cd $ROOT_OF_OSCAR_REPO
#  docker build -t $IMAGE_NAME \
#     --build-arg FIRESTORE_DB=prod \
#     --build-arg ENABLE_SYNC=true \
#     --build-arg ENABLE_CHANGES=false \
#     .

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

# To pass flag values to the command from docker build args, we must set environment
# variables. The ARG names cannot be substituted into a CMD.

ARG FIRESTORE_DB
ENV FIRESTORE_DB=$FIRESTORE_DB

ARG ENABLE_SYNC=false
ENV ENABLE_SYNC=$ENABLE_SYNC

ARG ENABLE_CHANGES=false
ENV ENABLE_CHANGES=$ENABLE_CHANGES

CMD ./gaby -firestoredb $FIRESTORE_DB -enablesync=$ENABLE_SYNC -enablechanges=$ENABLE_CHANGES

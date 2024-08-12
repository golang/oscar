#!/bin/bash
# Copyright 2024 The Go Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# Run worksync.sh to sync the various go.work, go.work.sum, go.mod, and go.sum files
# after changing dependencies.

set -e

syncmod() {
	dir=$1
	oscars=$(GOWORK=off go -C $dir list -m ... | awk 'NR>1 && /^golang.org\/x\/oscar/ { print $1 "@master" }')
	if [ "$oscars" != "" ]; then
		go -C $dir get $oscars
	fi
}

tidymod() {
	dir=$1
	go -C $dir mod tidy
}

moddirs="$(find . -name go.mod | sed 's;/go.mod;;')"

for dir in $moddirs
do
	syncmod $dir
done

go work sync

for dir in $moddirs
do
	tidymod $dir
done

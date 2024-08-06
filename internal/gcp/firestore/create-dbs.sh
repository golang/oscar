#!/bin/bash -e

# This script creates Firestore databases for Oscar.
# It takes no arguments, but requires that the OSCAR_PROJECT
# environment variable is defined.
#
# It creates three databases:
#   test    for unit and integration tests
#   devel   for experimentation (not dev; DB names must be >= 4 chars)
#   prod    for production

if [[ $OSCAR_PROJECT = '' ]]; then
	echo >&2 "set env var OSCAR_PROJECT to the ID of the GCP project"
	exit 2
fi

for env in test devel prod; do
	(set -x;
	# nam5 is a multi-region location in the central US.
	# Delete protection prevents database deletion.
	# PITR is point-in-time recovery:
	#   https://firebase.google.com/docs/firestore/pitr
	gcloud firestore databases create \
		--project $OSCAR_PROJECT \
		--database $env \
		--location nam5 \
		--delete-protection \
		--enable-pitr
	)
done

# The test DB requires this index for the vector test (see internal/storage/vtest.go).
gcloud alpha firestore indexes composite create \
	--project=$OSCAR_PROJECT --database=test --collection-group=vectors --query-scope=COLLECTION \
	--field-config=vector-config='{"dimension":"16","flat": "{}"}',field-path=Embedding


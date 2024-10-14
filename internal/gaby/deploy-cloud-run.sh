#!/bin/bash

set -e

# This command builds a docker container for gaby that refers the Firestore "devel"
# database, and deploys it as the Cloud Run service "gaby-devel".
# It takes no arguments.
#
# The script requires the OSCAR_PROJECT environment variable to be set to the project
# ID of the GCP project hosting Oscar.
#
# The script passes false for both the -enablesync and -enablechanges flags to gaby.
# Edit the script to pass true for one or both of them.
#
# This script requires the following GCP permissions, and possibly others:
#     Cloud Run Developer

if [[ $OSCAR_PROJECT = '' ]]; then
	echo >&2 "set env var OSCAR_PROJECT to the ID of the GCP project"
	exit 2
fi

# Variables must be defined.
set -u

region=us-central1

image=gcr.io/$OSCAR_PROJECT/gaby:devel

repo_root=$(git rev-parse --show-toplevel)

set -x

docker build -f internal/gaby/Dockerfile \
	-t $image \
	--build-arg FIRESTORE_DB=devel \
	--build-arg ENABLE_SYNC=false \
	--build-arg ENABLE_CHANGES=false \
	$repo_root

docker push $image

gcloud run deploy gaby-devel --image $image --region $region --memory 4G --quiet
gcloud run services update-traffic gaby-devel --to-latest --region $region


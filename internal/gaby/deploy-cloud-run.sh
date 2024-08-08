#!/bin/bash

set -e

# This script deploys gaby to Cloud Run.
# It takes a single argument, the Firestore database name, which
# is also the suffix of the Cloud Run service name.
#
# Example:
#
#    deploy-cloud-run.sh devel
#
# This command builds a docker container for gaby that refers the Firestore "devel"
# database, and deploys it as the Cloud Run service "gaby-devel".
#
# The script requires the OSCAR_PROJECT environment variable to be set to the project
# ID of the GCP project hosting Oscar.
#
# The script passes false for both the -enablesync and -enablechanges flags to gaby.
# Edit the script to pass true for one or both of them.
#
# You should avoid using this script to deploy to prod unless you know what you are doing.
# And you should avoid deploying anywhere from a workspace with uncommitted files;
# when debugging, it may be impossible to retrieve the exact source that produced the container.
#
# This script requires the following GCP permissions, and possibly others:
#     Cloud Run Developer

if [[ $OSCAR_PROJECT = '' ]]; then
	echo >&2 "set env var OSCAR_PROJECT to the ID of the GCP project"
	exit 2
fi

firestore_db=$1
case $firestore_db in
  devel|prod);;
  *)
	echo >&2 "usage: $0 FIRESTORE_DB"
	echo >&2 "       FIRESTORE_DB must be one of: devel, prod"
	exit 2
esac

image=gcr.io/$OSCAR_PROJECT/gaby:$firestore_db

repo_root=$(git rev-parse --show-toplevel)

set -x

docker build -f internal/gaby/Dockerfile \
	-t $image \
	--build-arg FIRESTORE_DB=$firestore_db \
	--build-arg ENABLE_SYNC=false \
	--build-arg ENABLE_CHANGES=false \
	$repo_root

docker push $image

gcloud run deploy gaby-$firestore_db --image $image --quiet


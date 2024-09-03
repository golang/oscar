// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Crproxy is an AppEngine service that proxies requests
to a Cloud Run service.

The app.yaml for this service should specify two environment variables:
CLOUD_RUN_HOST, the hostname of the Cloud Run service;
and JWT_AUDIENCE, the JWT audience code for the App Engine service,
which can be found on https://console.corp.google.com/security/iap in the dropdown
menu to the right of the App Engine row.

An example of a complete app.yaml is:

	runtime: go122
	env_variables:
	    CLOUD_RUN_HOST: my-cloud-run-service.run.app
	    JWT_AUDIENCE: my-jwt-audience
*/
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"

	"cloud.google.com/go/compute/metadata"
	"cloud.google.com/go/firestore"
	"golang.org/x/oscar/internal/gcp/gcphandler"
	"google.golang.org/api/idtoken"
)

func main() {
	lg := slog.New(gcphandler.New(slog.LevelDebug))
	lg.Info("starting")
	port := os.Getenv("PORT")
	if port == "" {
		lg.Error("PORT undefined")
		os.Exit(2)
	}
	cloudRunHost := os.Getenv("CLOUD_RUN_HOST")
	if cloudRunHost == "" {
		lg.Error("missing environment variable CLOUD_RUN_HOST; should be set in app.yaml")
		os.Exit(2)
	}
	jwtAudience := os.Getenv("JWT_AUDIENCE")
	if jwtAudience == "" {
		lg.Error("missing environment variable JWT_AUDIENCE; should be set in app.yaml")
		os.Exit(2)
	}

	// Create a client that passes the necessary credentials to the Cloud Run service.
	// This App Engine app's service account should have the Cloud Run Invoker role.
	// The only other piece of the credential is the audience value for the Cloud Run service,
	// which is just its URL.
	ctx := context.Background()
	idClient, err := idtoken.NewClient(ctx, "https://"+cloudRunHost)
	if err != nil {
		lg.Error("idtoken.NewClient", "err", err)
		os.Exit(2)
	}
	target := &url.URL{
		Scheme: "https",
		Host:   cloudRunHost,
	}

	// Create a Firestore client for reading auth information.
	fsClient, err := firestoreClient(ctx, "")
	if err != nil {
		lg.Error("firestore.NewClient", "err", err)
		os.Exit(2)
	}
	defer fsClient.Close()

	// Read the role maps at startup to check if they are valid.
	if _, _, err := readFirestoreRoles(ctx, fsClient); err != nil {
		lg.Error("readRoles", "err", err)
		os.Exit(1)
	}

	authFunc := func(user, urlPath string) (bool, error) {
		// Re-Read the role maps each time in case they've been updated.
		userMap, pathMap, err := readFirestoreRoles(ctx, fsClient)
		if err != nil {
			return false, err
		}
		return isAuthorized(user, urlPath, userMap, pathMap), nil
	}

	// Create a reverse proxy to the Cloud Run host.
	rp := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			// Keep SetURL's rewrite of the outbound Host header;
			// we get a 404 if the header is preserved.
		},
		Transport: idClient.Transport,
		ErrorLog:  slog.NewLogLogger(lg.Handler(), slog.LevelError),
	}

	mux := http.NewServeMux()
	mux.Handle("/", iapAuth(lg, jwtAudience, authFunc, rp))
	lg.Info("listening", "port", port)
	lg.Error("ListenAndServe", "err", http.ListenAndServe(":"+port, mux))
	os.Exit(1)
}

// iapAuth validates the JWT token passed by IAP.
// This is required to secure the app. See https://cloud.google.com/iap/docs/identity-howto.
//
// Based on x/website/cmd/adminapp/main.go.
func iapAuth(lg *slog.Logger, audience string, authorized func(string, string) (bool, error), h http.Handler) http.Handler {
	// See https://cloud.google.com/iap/docs/signed-headers-howto#verifying_the_jwt_payload.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jwt := r.Header.Get("x-goog-iap-jwt-assertion")
		if jwt == "" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, "must run under IAP\n")
			return
		}
		user, err := validateJWT(r.Context(), jwt, audience)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			lg.Warn("IAP validation", "err", err)
			return
		}
		auth, err := authorized(user, r.URL.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			lg.Error("authorizing", "err", err)
			return
		}
		if !auth {
			http.Error(w, "ACLs forbid access", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func firestoreClient(ctx context.Context, projectID string) (*firestore.Client, error) {
	if projectID == "" {
		var err error
		projectID, err = metadata.ProjectIDWithContext(ctx)
		if err != nil {
			return nil, err
		}
		if projectID == "" {
			return nil, errors.New("metadata.ProjectID is empty")
		}
	}
	return firestore.NewClient(ctx, projectID)
}

// validateJWT validates a JWT token.
// It also checks that IAP issued the token, that its lifetime is valid
// (it was issued before, and expires after, the current time, with some slack),
// and that it contains a valid "email" claim.
func validateJWT(ctx context.Context, jwt, audience string) (string, error) {
	payload, err := idtoken.Validate(ctx, jwt, audience)
	if err != nil {
		return "", fmt.Errorf("idtoken.Validate: %v", err)
	}
	if payload.Issuer != "https://cloud.google.com/iap" {
		return "", fmt.Errorf("incorrect issuer: %q", payload.Issuer)
	}
	if payload.Expires+30 < time.Now().Unix() || payload.IssuedAt-30 > time.Now().Unix() {
		return "", errors.New("bad JWT token times")
	}
	user, ok := payload.Claims["email"].(string)
	if !ok {
		return "", errors.New("'email' claim not a string")
	}
	if user == "" {
		return "", errors.New("JWT missing 'email' claim")
	}
	return user, nil
}

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
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"

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
	idClient, err := idtoken.NewClient(context.Background(), "https://"+cloudRunHost)
	if err != nil {
		lg.Error("idtoken.NewClient", "err", err)
		os.Exit(2)
	}
	target := &url.URL{
		Scheme: "https",
		Host:   cloudRunHost,
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
	mux.Handle("/", iapAuth(lg, jwtAudience, rp))
	lg.Info("listening", "port", port)
	lg.Error("ListenAndServe", "err", http.ListenAndServe(":"+port, mux))
	os.Exit(1)
}

// Copied with minor modifications from from x/website/cmd/adminapp/main.go.
// It shouldn't be necessary to perform this extra check on App Engine,
// but it can't hurt.
func iapAuth(lg *slog.Logger, audience string, h http.Handler) http.Handler {
	// https://cloud.google.com/iap/docs/signed-headers-howto#verifying_the_jwt_payload
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jwt := r.Header.Get("x-goog-iap-jwt-assertion")
		if jwt == "" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, "must run under IAP\n")
			return
		}

		payload, err := idtoken.Validate(r.Context(), jwt, audience)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			lg.Warn("JWT validation error", "err", err)
			return
		}
		if payload.Issuer != "https://cloud.google.com/iap" {
			w.WriteHeader(http.StatusUnauthorized)
			lg.Warn("Incorrect issuer", "issuer", payload.Issuer)
			return
		}
		if payload.Expires+30 < time.Now().Unix() || payload.IssuedAt-30 > time.Now().Unix() {
			w.WriteHeader(http.StatusUnauthorized)
			lg.Warn("Bad JWT times",
				"expires", time.Unix(payload.Expires, 0),
				"issued", time.Unix(payload.IssuedAt, 0))
			return
		}
		h.ServeHTTP(w, r)
	})
}

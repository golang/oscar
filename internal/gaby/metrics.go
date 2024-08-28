// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	ometric "go.opentelemetry.io/otel/metric"
	"golang.org/x/oscar/internal/storage/timed"
)

// newCounter creates an integer counter instrument.
// It panics if the counter cannot be created.
func (g *Gaby) newCounter(name, description string) ometric.Int64Counter {
	c, err := g.meter.Int64Counter(metricName(name), ometric.WithDescription(description))
	if err != nil {
		g.slog.Error("counter creation failed", "name", name)
		panic(err)
	}
	return c
}

// newEndpointCounter creates an integer counter instrument, intended
// to count the number of times the given endpoint is requested.
// It panics if the counter cannot be created.
func (g *Gaby) newEndpointCounter(endpoint string) ometric.Int64Counter {
	name, desc := fmt.Sprintf("%ss", endpoint), fmt.Sprintf("number of /%s requests", endpoint)
	return g.newCounter(name, desc)
}

// registerWatcherMetric adds a metric called "watcher-latest" for the latest times of Watchers.
// The latests map contains the functions to compute the latest times, each labeled
// by a string which becomes the value of the "name" attribute in the metric.
func (g *Gaby) registerWatcherMetric(latests map[string]func() timed.DBTime) {
	_, err := g.meter.Int64ObservableGauge(metricName("watcher-latest"),
		ometric.WithDescription("latest DBTime of watcher"),
		ometric.WithInt64Callback(func(_ context.Context, observer ometric.Int64Observer) error {
			for name, f := range latests {
				observer.Observe(int64(f()), ometric.WithAttributes(attribute.String("name", name)))
			}
			return nil
		}))
	if err != nil {
		g.slog.Error("watcher gauge creation failed")
		panic(err)
	}
}

// metricName returns the full metric name for the given short name.
// The names are chosen to display nicely on the Metric Explorer's "select a metric"
// dropdown. Production metrics will group under "Gaby", while others will
// have their own, distinct groups.
func metricName(shortName string) string {
	if flags.firestoredb == "prod" {
		return "gaby/" + shortName
	}
	// Using a hyphen or slash after "gaby" puts the metric in the "Gaby" group.
	// We want non-prod metrics to be in a different group.
	return "gaby_" + flags.firestoredb + "/" + shortName
}

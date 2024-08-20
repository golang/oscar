// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gcpmetrics supports gathering and publishing metrics
// on GCP using OpenTelemetry.
package gcpmetrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	gcpexporter "github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric"
	"go.opentelemetry.io/contrib/detectors/gcp"
	ometric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

// The meter for creating metric instruments (counters and so on).
var meter ometric.Meter
var logger *slog.Logger

func Init(ctx context.Context, lg *slog.Logger, projectID string) (shutdown func(), err error) {
	// Create an exporter to send metrics to the GCP Monitoring service.
	ex, err := gcpexporter.New(gcpexporter.WithProjectID(projectID))
	if err != nil {
		return nil, err
	}
	// Wrap it with logging so we can see in the logs that data is being sent.
	lex := &loggingExporter{lg, ex}
	// By default, the PeriodicReader will export metrics once per minute.
	r := sdkmetric.NewPeriodicReader(lex)

	// Construct a Resource, which identifies the source of the metrics to GCP.
	// Although Cloud Run has its own resource type, user-defined metrics cannot use it.
	// Instead the gcp detector will put our metrics in the Generic Task group.
	// It doesn't really matter, as long as you know where to look.
	res, err := resource.New(ctx, resource.WithDetectors(gcp.NewDetector()))
	if errors.Is(err, resource.ErrPartialResource) || errors.Is(err, resource.ErrSchemaURLConflict) {
		lg.Warn("resource.New non-fatal error", "err", err)
	} else if err != nil {
		return nil, err
	}
	lg.Info("creating OTel MeterProvider", "resource", res.String())
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(r),
	)
	logger = lg
	meter = mp.Meter("gcp")
	return func() {
		if err := mp.Shutdown(context.Background()); err != nil {
			lg.Warn("metric shutdown failed", "err", err)
		}
	}, nil
}

// NewCounter creates an integer counter instrument.
// It panics if the counter cannot be created.
func NewCounter(name, description string) ometric.Int64Counter {
	c, err := meter.Int64Counter(name, ometric.WithDescription(description))
	if err != nil {
		logger.Error("counter creation failed", "name", name)
		panic(err)
	}
	return c
}

// A loggingExporter wraps an [sdkmetric.Exporter] with logging.
type loggingExporter struct {
	lg *slog.Logger
	sdkmetric.Exporter
}

// For testing.
var totalExports, failedExports atomic.Int64

func (e *loggingExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	var b strings.Builder
	for _, sm := range rm.ScopeMetrics {
		fmt.Fprintf(&b, "scope=%+v", sm.Scope)
		for _, m := range sm.Metrics {
			fmt.Fprintf(&b, " %q", m.Name)
		}
	}
	e.lg.Debug("start metric export",
		"resource", rm.Resource.String(),
		"metrics", b.String(),
	)
	err := e.Exporter.Export(ctx, rm)
	totalExports.Add(1)
	if err != nil {
		e.lg.Warn("metric export failed", "err", err)
		failedExports.Add(1)
	} else {
		e.lg.Debug("end metric export")
	}
	return err
}

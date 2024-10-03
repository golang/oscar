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
	"time"

	gcpexporter "github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric"
	"go.opentelemetry.io/contrib/detectors/gcp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

// NewMeterProvider creates an [sdkmetric.MeterProvider] that exports metrics to GCP's Monitoring service.
// Call Shutdown on the MeterProvider after use.
func NewMeterProvider(ctx context.Context, lg *slog.Logger, projectID string) (*sdkmetric.MeterProvider, error) {
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
	return sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(r),
	), nil
}

// A loggingExporter wraps an [sdkmetric.Exporter] with logging.
type loggingExporter struct {
	lg *slog.Logger
	sdkmetric.Exporter
}

// For testing.
var totalExports, failedExports atomic.Int64

// If true, print what would be exported but do not export.
const debug = false

func (e *loggingExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	totalExports.Add(1)
	if debug {
		debugExport(rm)
		return nil
	}
	err := e.Exporter.Export(ctx, rm)
	if err != nil {
		e.lg.Warn("metric export failed", "err", err)
		failedExports.Add(1)
	} else {
		var metricNames []string
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				metricNames = append(metricNames, m.Name)
			}
		}
		e.lg.Debug("exported metrics", "metrics", strings.Join(metricNames, " "))
	}
	return err
}

func debugExport(rm *metricdata.ResourceMetrics) {
	fmt.Println("DEBUG METRICS EXPORT (not actually exporting)")
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			fmt.Printf("metric %q\n", m.Name)
			switch d := m.Data.(type) {
			case metricdata.Gauge[int64]:
				debugDataPoints(d.DataPoints)
			case metricdata.Sum[int64]:
				debugDataPoints(d.DataPoints)
			default:
				fmt.Printf("\tdata of type %T\n", m.Data)
			}
		}
		fmt.Println("DEBUG END")
	}
}

func debugDataPoints[T int64 | float64](dps []metricdata.DataPoint[T]) {
	for _, dp := range dps {
		var as []string
		for _, kv := range dp.Attributes.ToSlice() {
			as = append(as, fmt.Sprintf("%s:%q", kv.Key, kv.Value.Emit()))
		}
		fmt.Printf("\ttime=%s value=%v attrs:{%s}\n",
			dp.Time.Format(time.DateTime), dp.Value, strings.Join(as, ", "))
	}
}

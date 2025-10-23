package metrics

import (
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
)

var (
	// Ingestion metrics
	eventsReceived = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "streamflow_events_received_total",
			Help: "Total number of events received",
		},
		[]string{"status"},
	)

	ingestionLatency = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "streamflow_ingestion_latency_seconds",
			Help:    "Ingestion latency in seconds",
			Buckets: prometheus.DefBuckets,
		},
	)

	// Processing metrics
	eventsQueued = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "streamflow_events_queued_total",
			Help: "Total number of events queued for processing",
		},
	)

	eventsDropped = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "streamflow_events_dropped_total",
			Help: "Total number of events dropped",
		},
	)

	eventsProcessed = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "streamflow_events_processed_total",
			Help: "Total number of events processed",
		},
	)

	processingLatency = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "streamflow_processing_latency_seconds",
			Help:    "Processing latency in seconds",
			Buckets: prometheus.DefBuckets,
		},
	)

	// Storage metrics
	batchesWritten = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "streamflow_batches_written_total",
			Help: "Total number of batches written to storage",
		},
	)

	eventsWritten = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "streamflow_events_written_total",
			Help: "Total number of events written to storage",
		},
	)

	batchWriteErrors = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "streamflow_batch_write_errors_total",
			Help: "Total number of batch write errors",
		},
	)
)

type MetricsServer struct {
	port int
}

func NewServer(port int) *MetricsServer {
	return &MetricsServer{
		port: port,
	}
}

func (s *MetricsServer) Start() error {
	http.Handle("/metrics", promhttp.Handler())
	
	addr := fmt.Sprintf(":%d", s.port)
	log.Info().Msgf("Metrics server listening on %s", addr)
	
	return http.ListenAndServe(addr, nil)
}

// Helper functions to record metrics

func IncEventsReceived(status string) {
	eventsReceived.WithLabelValues(status).Inc()
}

func RecordIngestionLatency(seconds float64) {
	ingestionLatency.Observe(seconds)
}

func IncEventsQueued() {
	eventsQueued.Inc()
}

func IncEventsDropped() {
	eventsDropped.Inc()
}

func IncEventsProcessed() {
	eventsProcessed.Inc()
}

func RecordProcessingLatency(seconds float64) {
	processingLatency.Observe(seconds)
}

func IncBatchesWritten(eventCount int) {
	batchesWritten.Inc()
	eventsWritten.Add(float64(eventCount))
}

func IncBatchWriteErrors() {
	batchWriteErrors.Inc()
}

func IncBatchEventsReceived(accepted, failed int) {
	eventsReceived.WithLabelValues("accepted").Add(float64(accepted))
	eventsReceived.WithLabelValues("dropped").Add(float64(failed))
}


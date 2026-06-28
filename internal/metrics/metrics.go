package metrics

import (
	"alvus/internal/keypool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds all Prometheus metric handles for the application.
type Metrics struct {
	RequestsTotal   *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec
	KeyPoolKeys     *prometheus.GaugeVec
	UpstreamErrors  *prometheus.CounterVec
}

// NewRegistry creates a non-global Prometheus registry and registers all application metrics.
// Returns the registry, the Metrics handles, and a factory for auto-registration.
func NewRegistry() (*prometheus.Registry, *Metrics) {
	reg := prometheus.NewRegistry()

	factory := promauto.With(reg)

	m := &Metrics{
		RequestsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "alvus_requests_total",
				Help: "Total number of proxy requests by method, status class, and key index.",
			},
			[]string{"method", "status", "key_index"},
		),
		RequestDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "alvus_request_duration_seconds",
				Help:    "Request latency distribution by method and status class.",
				Buckets: prometheus.DefBuckets, // .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10
			},
			[]string{"method", "status"},
		),
		KeyPoolKeys: factory.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "alvus_keypool_keys",
				Help: "Current number of keys by state (active, cooling, disabled).",
			},
			[]string{"state"},
		),
		UpstreamErrors: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "alvus_upstream_errors_total",
				Help: "Count of upstream errors by type (network, rate_limited, auth_rejected, server_error).",
			},
			[]string{"type"},
		),
	}

	// Register Go runtime metrics (go_*, process_*) as well
	reg.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	reg.MustRegister(prometheus.NewGoCollector())

	return reg, m
}

// RefreshKeyPoolGauge updates the KeyPoolKeys gauge from the pool's current state.
// Call this periodically (e.g. every 10 seconds).
func (m *Metrics) RefreshKeyPoolGauge(pool *keypool.KeyPool) {
	m.KeyPoolKeys.WithLabelValues("active").Set(float64(pool.ActiveCount()))
	m.KeyPoolKeys.WithLabelValues("cooling").Set(float64(pool.CoolingCount()))
	m.KeyPoolKeys.WithLabelValues("disabled").Set(float64(pool.DisabledCount()))
}

// StatusLabel converts an HTTP status code to a Prometheus-compatible status class label.
func StatusLabel(code int) string {
	switch {
	case code < 200:
		return "1xx"
	case code < 300:
		return "2xx"
	case code < 400:
		return "3xx"
	case code < 500:
		return "4xx"
	default:
		return "5xx"
	}
}

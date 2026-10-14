package main

import (
	"net/http"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// newMetrics builds a registry of our own rather than using the package's
// global one, so tests can build as many servers as they like without a
// "duplicate metrics collector registration" panic. The counts live in the
// hub, the bus and the limiter; the registry only reads them when scraped.
// Any of the three is nil in tests that do not need it.
func newMetrics(hub *Hub, b *bus, l *limiter) *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	if hub != nil {
		reg.MustRegister(
			// Read from the hub at scrape time: a counter moved on connect and
			// disconnect could drift from the hub after a missed path, this cannot.
			prometheus.NewGaugeFunc(prometheus.GaugeOpts{
				Name: "messenger_sockets",
				Help: "WebSocket connections held by this node.",
			}, func() float64 {
				sockets, _ := hub.Counts()
				return float64(sockets)
			}),
			prometheus.NewGaugeFunc(prometheus.GaugeOpts{
				Name: "messenger_users_connected",
				Help: "Distinct users with at least one socket on this node.",
			}, func() float64 {
				_, users := hub.Counts()
				return float64(users)
			}),
			counterOf("messenger_frames_sent_total",
				"Frames handed to a socket's send buffer.", &hub.framesSent),
			counterOf("messenger_sockets_dropped_slow_total",
				"Sockets dropped because their send buffer was full.", &hub.slowDropped),
		)
	}
	if b != nil {
		reg.MustRegister(
			counterOf("messenger_bus_published_total",
				"Publishes this node sent to Redis.", &b.published),
			counterOf("messenger_bus_publish_failures_total",
				"Publishes Redis did not take; every node is told to resync later.", &b.publishFailed),
			counterOf("messenger_bus_received_total",
				"Channel frames this node received from Redis, its own included.", &b.received),
			counterOf("messenger_bus_gaps_total",
				"Times this node told its sockets to resync after a possible loss.", &b.gaps),
		)
		if b.deliveryLatency != nil {
			reg.MustRegister(b.deliveryLatency)
		}
	}
	if l != nil {
		reg.MustRegister(refusalCollector{l})
	}
	return reg
}

// newDeliveryLatency expects single milliseconds inside one region; the tail
// up to seconds is there to show GC pauses and a slow Redis.
func newDeliveryLatency() prometheus.Histogram {
	return prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "messenger_delivery_latency_seconds",
		Help:    "From a message's created_at to its arrival on this node through Redis.",
		Buckets: []float64{.0005, .001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5},
		// Served alongside the classic buckets: the collector on AWS turns only
		// the native form into values and counts CloudWatch can take percentiles
		// of, while a plain text scrape still sees the buckets. Each native
		// bucket is about 10% wider than the last, at most 100 of them.
		NativeHistogramBucketFactor:    1.1,
		NativeHistogramMaxBucketNumber: 100,
	})
}

func counterOf(name, help string, n *atomic.Uint64) prometheus.CounterFunc {
	return prometheus.NewCounterFunc(prometheus.CounterOpts{Name: name, Help: help},
		func() float64 { return float64(n.Load()) })
}

var refusedDesc = prometheus.NewDesc("messenger_rate_limited_total",
	"Requests refused with 429, by limit.", []string{"limit"}, nil)

// refusalCollector reports one series per limit that has refused anything.
// The limits are package variables with no list of them, so the series come
// from what the limiter has seen rather than from a list that could fall behind.
type refusalCollector struct{ l *limiter }

func (c refusalCollector) Describe(ch chan<- *prometheus.Desc) { ch <- refusedDesc }

func (c refusalCollector) Collect(ch chan<- prometheus.Metric) {
	c.l.refused.Range(func(name, n any) bool {
		ch <- prometheus.MustNewConstMetric(refusedDesc, prometheus.CounterValue,
			float64(n.(*atomic.Uint64).Load()), name.(string))
		return true
	})
}

func metricsHandler(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}

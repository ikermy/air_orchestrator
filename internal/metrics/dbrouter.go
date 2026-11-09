package metrics

import (
	"github.com/ikermy/air-common/v2/pkg/dbrouter"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	dbRouterRequestsDesc = prometheus.NewDesc(
		"air_common_db_router_requests_total",
		"Total shard resolutions performed by the DB router.",
		[]string{"shard"}, nil,
	)
	dbRouterErrorsDesc = prometheus.NewDesc(
		"air_common_db_router_errors_total",
		"Total DB router errors (resolution or connection).",
		[]string{"shard"}, nil,
	)
	dbRouterGenerationDesc = prometheus.NewDesc(
		"air_common_db_router_generation",
		"Active DB router slot-map generation.",
		nil, nil,
	)
)

type dbRouterCollector struct {
	router *dbrouter.Router
}

// RegisterDBRouter exposes DB router counters via the default registry.
// Safe to call once per process.
func RegisterDBRouter(router *dbrouter.Router) {
	if router == nil {
		return
	}
	if err := prometheus.Register(&dbRouterCollector{router: router}); err != nil {
		if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
			panic(err)
		}
	}
}

func (c *dbRouterCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- dbRouterRequestsDesc
	ch <- dbRouterErrorsDesc
	ch <- dbRouterGenerationDesc
}

func (c *dbRouterCollector) Collect(ch chan<- prometheus.Metric) {
	m := c.router.Metrics()
	for shard, total := range m.ByShard {
		ch <- prometheus.MustNewConstMetric(dbRouterRequestsDesc, prometheus.CounterValue, float64(total), shard)
	}
	for shard, total := range m.ErrorsByShard {
		ch <- prometheus.MustNewConstMetric(dbRouterErrorsDesc, prometheus.CounterValue, float64(total), shard)
	}
	ch <- prometheus.MustNewConstMetric(dbRouterGenerationDesc, prometheus.GaugeValue, float64(m.Generation))
}

package responseobs

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// largeResponsesCounter increments once per Observation that crosses a
// configured threshold. Cardinality is self-limiting because the counter
// only increments on threshold crossings — the number of active series is
// bounded by the number of datasource instances actually producing large
// responses, not by total query volume.
//
// The app_url label carries the stack identifier. It replaces the earlier
// "slug" label in the plan because backend.GrafanaConfig exposes no
// dedicated slug accessor; downstream operators can derive a slug by
// parsing the URL if needed.
var largeResponsesCounter = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "plugins",
	Name:      "sql_large_responses_total",
	Help:      "Number of SQL datasource responses that crossed a configured size threshold (rows or bytes).",
}, []string{"datasource_type", "app_url", "datasource_uid"})

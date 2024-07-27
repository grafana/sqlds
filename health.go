package sqlds

import (
	"context"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

type HealthChecker struct {
	Connector *Connector
	Metrics   Metrics
}

func (hc *HealthChecker) Check(ctx context.Context, req *backend.CheckHealthRequest, ds *SQLDatasource) (*backend.CheckHealthResult, error) {
	start := time.Now()
	_, err := hc.Connector.Connect(ctx, req.GetHTTPHeaders())
	if err != nil {
		hc.Metrics.CollectDuration(SourceDownstream, StatusError, time.Since(start).Seconds())
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: err.Error(),
		}, DownstreamError(err)
	}

	if checkHealthMutator, ok := ds.driver().(CheckHealthMutator); ok {
		// If you need to use ctx and req after this point you can grab it from the return values
		checkHealthMutator.MutateCheckHealth(ctx, req)
	}
	hc.Metrics.CollectDuration(SourceDownstream, StatusOK, time.Since(start).Seconds())

	return &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: "Data source is working",
	}, nil
}

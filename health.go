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

func (hc *HealthChecker) Check(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	start := time.Now()

	// TODO: 2 This starts the connection after CheckHealth is called.
	// after this line the headers are now forwarded
	_, err := hc.Connector.Connect(ctx, req.GetHTTPHeaders())
	// TODO: 3 Move checkealthMutator here
	// because after this point we have the most current headers
	//
	// TODO: 4 but if we move it here then it'd be a breaking change for other sqlds clients
	if err != nil {
		hc.Metrics.CollectDuration(SourceDownstream, StatusError, time.Since(start).Seconds())
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: err.Error(),
		}, DownstreamError(err)
	}
	hc.Metrics.CollectDuration(SourceDownstream, StatusOK, time.Since(start).Seconds())

	return &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: "Data source is working",
	}, nil
}

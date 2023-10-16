[![Build Status](https://drone.grafana.net/api/badges/grafana/sqlds/status.svg)](https://drone.grafana.net/grafana/sqlds)

# sqlds

`sqlds` stands for `SQL Datasource`.

Most SQL-driven datasources, like `Postgres`, `MySQL`, and `MSSQL` share extremely similar codebases.

The `sqlds` package is intended to remove the repetition of these datasources and centralize the datasource logic. The only thing that the datasources themselves should have to define is connecting to the database, and what driver to use, and the plugin frontend.

**Usage**

```go
if err := datasource.Manage("my-datasource", datasourceFactory, datasource.ManageOpts{}); err != nil {
  log.DefaultLogger.Error(err.Error())
  os.Exit(1)
}

func datasourceFactory(ctx context.Context, s backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
  ds := sqlds.NewDatasource(&myDatasource{})
  return ds.NewDatasource(ctx, s)
}
```

## Standardization

### Macros

The `sqlds` package defines a set of default macros:

- `$__timeFilter(time_column)`: Filters by timestamp using the query period. Resolves to: `time >= '0001-01-01T00:00:00Z' AND time <= '0001-01-01T00:00:00Z'`
- `$__timeFrom(time_column)`: Filters by timestamp using the start point of the query period. Resolves to `time >= '0001-01-01T00:00:00Z'`
- `$__timeTo(time_column)`: Filters by timestamp using the end point of the query period. Resolves to `time <= '0001-01-01T00:00:00Z'`
- `$__timeGroup(time_column, period)`: To group times based on a period. Resolves to (minute example): `"datepart(year, time), datepart(month, time)'"`
- `$__table`: Returns the `table` configured in the query.
- `$__column`: Returns the `column` configured in the query.

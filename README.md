[![Build Status](https://drone.grafana.net/api/badges/grafana/sqlds/status.svg)](https://drone.grafana.net/grafana/sqlds)

# sqlds

`sqlds` stands for `SQL Datasource`.

Most SQL-driven datasources, like `Postgres`, `MySQL`, and `MSSQL` share extremely similar codebases.

The `sqlds` package is intended to remove the repetition of these datasources and centralize the datasource logic. The only thing that the datasources themselves should have to define is connecting to the database, and what driver to use, and the plugin frontend.

**Usage**

```go
ds := sqlds.NewDatasource(&myDatasource{})
if err := datasource.Manage("my-datasource", ds.NewDatasource, datasource.ManageOpts{}); err != nil {
  log.DefaultLogger.Error(err.Error())
  os.Exit(1)
}
```

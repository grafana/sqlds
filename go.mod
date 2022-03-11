module github.com/grafana/sqlds/v2

go 1.15

require (
	github.com/go-sql-driver/mysql v1.4.0
	github.com/google/go-cmp v0.5.6
	github.com/grafana/grafana-plugin-sdk-go v0.94.0
	github.com/patrickmn/go-cache v2.1.0+incompatible
	github.com/stretchr/testify v1.7.0
	golang.org/x/sys v0.0.0-20210615035016-665e8c7367d1 // indirect
)

replace github.com/grafana/grafana-plugin-sdk-go => ../grafana-plugin-sdk-go

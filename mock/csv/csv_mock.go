package csv

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
	"github.com/grafana/sqlds/v2"
	_ "github.com/mithrandie/csvq-driver"
)

// SQLCSVMock connects to a local folder with csv files
type SQLCSVMock struct {
	folder string
}

func (h *SQLCSVMock) Settings(config backend.DataSourceInstanceSettings) sqlds.DriverSettings {
	return sqlds.DriverSettings{
		FillMode: &data.FillMissing{
			Mode: data.FillModeNull,
		},
		Timeout: time.Second * time.Duration(30),
	}
}

// Connect opens a sql.DB connection using datasource settings
func (h *SQLCSVMock) Connect(config backend.DataSourceInstanceSettings, msg json.RawMessage) (*sql.DB, error) {
	backend.Logger.Debug("connecting to mock data")
	folder := h.folder
	if folder == "" {
		folder = MockDataFolder
	}
	if !strings.HasPrefix(folder, "/") {
		folder = "/" + folder
	}
	err := CreateMockTable("users", folder)
	if err != nil {
		backend.Logger.Error("failed creating mock data: " + err.Error())
		return nil, err
	}
	ex, err := os.Executable()
	if err != nil {
		backend.Logger.Error("failed accessing Mock path: " + err.Error())
	}
	exPath := filepath.Dir(ex)
	db, err := sql.Open("csvq", exPath+folder)
	if err != nil {
		backend.Logger.Error("failed opening Mock sql: " + err.Error())
		return nil, err
	}
	err = db.Ping()
	if err != nil {
		backend.Logger.Error("failed connecting to Mock: " + err.Error())
	}

	timeout := time.Duration(1999)
	ctx, cancel := context.WithTimeout(context.Background(), timeout*time.Second)
	defer cancel()

	chErr := make(chan error, 1)
	go func() {
		err = db.PingContext(ctx)
		duration := time.Second * 60
		time.Sleep(duration)
		chErr <- err
	}()

	select {
	case err := <-chErr:
		if err != nil {
			// log.DefaultLogger.Error(err.Error())
			return db, err
		}
	case <-time.After(timeout * time.Second):
		return db, errors.New("connection timed out")
	}
	return db, nil
}

// Converters defines list of string convertors
func (h *SQLCSVMock) Converters() []sqlutil.Converter {
	return []sqlutil.Converter{}
}

// Macros returns list of macro functions convert the macros of raw query
func (h *SQLCSVMock) Macros() sqlds.Macros {
	return sqlds.Macros{}
}

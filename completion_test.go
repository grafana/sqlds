package sqlds

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
)

func Test_handleError(t *testing.T) {
	t.Run("it should write an error code and a message", func(t *testing.T) {
		w := httptest.NewRecorder()
		handleError(w, fmt.Errorf("test!"))

		resp := w.Result()
		body, _ := io.ReadAll(resp.Body)

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expecting code %v got %v", http.StatusBadRequest, resp.StatusCode)
		}
		if string(body) != "test!" {
			t.Errorf("expecting response test! got %v", string(body))
		}
	})
}

func Test_sendResourceResponse(t *testing.T) {
	t.Run("it should send a JSON response", func(t *testing.T) {
		w := httptest.NewRecorder()
		sendResourceResponse(w, []string{"foo", "bar"})

		resp := w.Result()
		body, _ := io.ReadAll(resp.Body)

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expecting code %v got %v", http.StatusBadRequest, http.StatusOK)
		}
		expectedResult := `["foo","bar"]` + "\n"
		if string(body) != expectedResult {
			t.Errorf("expecting response %v got %v", expectedResult, string(body))
		}
		if resp.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expecting content-type application/json got %v", resp.Header.Get("Content-Type"))
		}
	})
}

type fakeCompletable struct {
	schemas map[string][]string
	tables  map[string][]string
	columns map[string][]string
	err     error
}

func (f *fakeCompletable) Schemas(ctx context.Context, options Options) ([]string, error) {
	return f.schemas[options["database"]], f.err
}

func (f *fakeCompletable) Tables(ctx context.Context, options Options) ([]string, error) {
	return f.tables[options["schema"]], f.err
}

func (f *fakeCompletable) Columns(ctx context.Context, options Options) ([]string, error) {
	return f.columns[options["table"]], f.err
}

func TestCompletable(t *testing.T) {
	tests := []struct {
		description string
		method      string
		fakeImpl    *fakeCompletable
		reqBody     string
		expectedRes string
	}{
		{
			"it should return schemas",
			"schemas",
			&fakeCompletable{schemas: map[string][]string{"foobar": {"foo", "bar"}}},
			`{"database":"foobar"}`,
			`["foo","bar"]` + "\n",
		},
		{
			"it should return tables of a schema",
			"tables",
			&fakeCompletable{tables: map[string][]string{"foobar": {"foo", "bar"}}},
			`{"schema":"foobar"}`,
			`["foo","bar"]` + "\n",
		},
		{
			"it should return columns of a table",
			"columns",
			&fakeCompletable{columns: map[string][]string{"foobar": {"foo", "bar"}}},
			`{"table":"foobar"}`,
			`["foo","bar"]` + "\n",
		},
	}
	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			w := httptest.NewRecorder()

			sqlds := &sqldatasource{}
			sqlds.Completable = test.fakeImpl

			b := ioutil.NopCloser(bytes.NewReader([]byte(test.reqBody)))
			switch test.method {
			case "schemas":
				sqlds.getSchemas(w, &http.Request{
					Body: b,
				})
			case "tables":
				sqlds.getTables(w, &http.Request{
					Body: b,
				})
			case "columns":
				sqlds.getColumns(w, &http.Request{
					Body: b,
				})
			}

			resp := w.Result()
			body, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != http.StatusOK {
				t.Errorf("expecting code %v got %v", http.StatusOK, resp.StatusCode)
			}
			if string(body) != test.expectedRes {
				t.Errorf("expecting response %v got %v", test.expectedRes, string(body))
			}
			if resp.Header.Get("Content-Type") != "application/json" {
				t.Errorf("expecting content-type application/json got %v", resp.Header.Get("Content-Type"))
			}
		})
	}
}

func Test_registerRoutes(t *testing.T) {
	t.Run("it should add a new route", func(t *testing.T) {
		sqlds := &sqldatasource{}
		sqlds.CustomRoutes = map[string]func(http.ResponseWriter, *http.Request){
			"/foo": func(w http.ResponseWriter, r *http.Request) {
				_, err := w.Write([]byte("bar"))
				if err != nil {
					t.Fatal((err))
				}
			},
		}

		mux := http.NewServeMux()
		err := sqlds.registerRoutes(mux)
		if err != nil {
			t.Fatalf("unexpected error %v", err)
		}
		resp := httptest.NewRecorder()
		req, err := http.NewRequest("GET", "/foo", nil)
		if err != nil {
			t.Fatalf("unexpected error %v", err)
		}
		mux.ServeHTTP(resp, req)

		respByte, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("unexpected error %v", err)
		}
		if string(respByte) != "bar" {
			t.Errorf("unexpected response %s", string(respByte))
		}
	})

	t.Run("it error if tried to add an existing route", func(t *testing.T) {
		sqlds := &sqldatasource{}
		sqlds.CustomRoutes = map[string]func(http.ResponseWriter, *http.Request){
			"/tables": func(w http.ResponseWriter, r *http.Request) {},
		}

		mux := http.NewServeMux()
		err := sqlds.registerRoutes(mux)
		if err == nil || err.Error() != "unable to redefine /tables, use the Completable interface instead" {
			t.Errorf("unexpected error %v", err)
		}
	})
}

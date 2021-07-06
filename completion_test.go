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
	tables  []string
	schemas []string
	columns map[string][]string
	err     error
}

func (f *fakeCompletable) Tables(ctx context.Context) ([]string, error) {
	return f.tables, f.err
}
func (f *fakeCompletable) Schemas(ctx context.Context) ([]string, error) {
	return f.schemas, f.err

}
func (f *fakeCompletable) Columns(ctx context.Context, table string) ([]string, error) {
	return f.columns[table], f.err
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
			"it should return tables",
			"tables",
			&fakeCompletable{tables: []string{"foo", "bar"}},
			"",
			`["foo","bar"]` + "\n",
		},
		{
			"it should return schemas",
			"schemas",
			&fakeCompletable{schemas: []string{"foo", "bar"}},
			"",
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

			switch test.method {
			case "tables":
				sqlds.getTables(w, &http.Request{})
			case "schemas":
				sqlds.getSchemas(w, &http.Request{})
			case "columns":
				b := ioutil.NopCloser(bytes.NewReader([]byte(test.reqBody)))
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

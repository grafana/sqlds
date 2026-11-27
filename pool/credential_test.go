package pool

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/grafana/sqlds/v5/pool/pooltest"
)

// fakeServer records the credential of each connection that opens and closes.
type fakeServer struct {
	mu       sync.Mutex
	opened   []string
	openedAt []time.Time
	closed   []string
	resetErr error
	invalid  bool
}

func (s *fakeServer) connect(full bool) func(context.Context, string) (driver.Conn, error) {
	return func(_ context.Context, credential string) (driver.Conn, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.opened = append(s.opened, credential)
		s.openedAt = append(s.openedAt, time.Now())
		c := &minimalConn{s: s, credential: credential}
		if full {
			return fullConn{c}, nil
		}
		return c, nil
	}
}

func (s *fakeServer) seen() (opened, closed []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.opened), slices.Clone(s.closed)
}

// minimalConn implements only driver.Conn.
type minimalConn struct {
	s          *fakeServer
	credential string
}

func (c *minimalConn) Prepare(string) (driver.Stmt, error) {
	return credentialStmt{c.credential}, nil
}

func (c *minimalConn) Begin() (driver.Tx, error) {
	return nil, errors.New("fake: no transactions")
}

func (c *minimalConn) Close() error {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	c.s.closed = append(c.s.closed, c.credential)
	return nil
}

// fullConn adds the optional interfaces that the tests check.
type fullConn struct {
	*minimalConn
}

func (c fullConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &credentialRows{credential: c.credential}, nil
}

func (c fullConn) ResetSession(context.Context) error {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	return c.s.resetErr
}

func (c fullConn) IsValid() bool {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	return !c.s.invalid
}

type credentialStmt struct {
	credential string
}

func (credentialStmt) Close() error  { return nil }
func (credentialStmt) NumInput() int { return 0 }

func (credentialStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}

func (s credentialStmt) Query([]driver.Value) (driver.Rows, error) {
	return &credentialRows{credential: s.credential}, nil
}

type credentialRows struct {
	credential string
	done       bool
}

func (*credentialRows) Columns() []string { return []string{"credential"} }
func (*credentialRows) Close() error      { return nil }

func (r *credentialRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = r.credential
	return nil
}

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("fake: open through the connector")
}

func queryCredential(t *testing.T, db *sql.DB) string {
	t.Helper()
	var got string
	if err := db.QueryRowContext(context.Background(), "SELECT credential").Scan(&got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestCredentialConnector(t *testing.T) {
	for _, tt := range []struct {
		name string
		full bool
	}{
		{"driver with optional interfaces", true},
		{"driver with only driver.Conn", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := &fakeServer{}
				var cred Credential
				db := sql.OpenDB(cred.Connector(fakeDriver{}, s.connect(tt.full)))
				defer func() { _ = db.Close() }()

				var queries []string
				query := func(credential string) {
					cred.Store(credential)
					queries = append(queries, queryCredential(t, db))
				}
				query("laptop-1")
				query("phone-1")
				query("laptop-1")
				time.Sleep(DefaultIdleTTL + time.Second)
				query("phone-1")
				if _, err := db.ExecContext(context.Background(), "SET x = 1"); err != nil {
					t.Fatal(err)
				}

				opened, closed := s.seen()
				if want := []string{"laptop-1", "laptop-1", "laptop-1", "phone-1"}; !slices.Equal(queries, want) {
					t.Errorf("queries ran with %v, want %v", queries, want)
				}
				if want := []string{"laptop-1", "phone-1"}; !slices.Equal(opened, want) {
					t.Errorf("opened %v, want %v", opened, want)
				}
				if want := []string{"laptop-1"}; !slices.Equal(closed, want) {
					t.Errorf("closed %v, want %v", closed, want)
				}
				if n := len(cred.stored); n != 1 {
					t.Errorf("Credential keeps %d credentials, want only the current one", n)
				}
			})
		})
	}
}

func TestCredentialConnectorClosesConnectionAtExpiry(t *testing.T) {
	for _, tt := range []struct {
		name     string
		lifetime time.Duration
		token    func(now time.Time) string
	}{
		{"opaque token", time.Hour, func(time.Time) string { return "opaque" }},
		{"JWT that expires in 30 minutes", 30 * time.Minute, func(now time.Time) string {
			return pooltest.UnsignedJWT(fmt.Sprintf(`{"exp":%d}`, now.Add(30*time.Minute).Unix()))
		}},
		{"JWT that expires in 2 hours", 2 * time.Hour, func(now time.Time) string {
			return pooltest.UnsignedJWT(fmt.Sprintf(`{"exp":%d}`, now.Add(2*time.Hour).Unix()))
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := &fakeServer{}
				var cred Credential
				db := sql.OpenDB(cred.Connector(fakeDriver{}, s.connect(true)))
				defer func() { _ = db.Close() }()

				start := time.Now()
				token := tt.token(start)
				for range 3 * 60 {
					cred.Store(token)
					queryCredential(t, db)
					if opened, _ := s.seen(); len(opened) > 1 {
						break
					}
					time.Sleep(time.Minute)
				}

				s.mu.Lock()
				defer s.mu.Unlock()
				if len(s.openedAt) != 2 {
					t.Fatalf("opened %d connections in 3 hours, want 2", len(s.openedAt))
				}
				if got := s.openedAt[1].Sub(start); got != tt.lifetime {
					t.Errorf("second connection opened after %s, want %s", got, tt.lifetime)
				}
			})
		})
	}
}

func TestCredentialConnectorKeepsDriverChecks(t *testing.T) {
	for _, tt := range []struct {
		name     string
		resetErr error
		invalid  bool
	}{
		{"ResetSession", driver.ErrBadConn, false},
		{"IsValid", nil, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := &fakeServer{resetErr: tt.resetErr, invalid: tt.invalid}
			var cred Credential
			cred.Store("token-1")
			db := sql.OpenDB(cred.Connector(fakeDriver{}, s.connect(true)))
			t.Cleanup(func() { _ = db.Close() })

			queryCredential(t, db)
			queryCredential(t, db)

			if opened, _ := s.seen(); len(opened) != 2 {
				t.Errorf("opened %v, want a new connection for each query", opened)
			}
		})
	}
}

func TestCredentialConnectorRejectsTransactionOptionsWithoutBeginTx(t *testing.T) {
	var cred Credential
	db := sql.OpenDB(cred.Connector(fakeDriver{}, (&fakeServer{}).connect(false)))
	t.Cleanup(func() { _ = db.Close() })

	_, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if !errors.Is(err, errTransactionOptions) {
		t.Errorf("BeginTx error = %v, want %v", err, errTransactionOptions)
	}
}

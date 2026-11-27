package pool

import (
	"net/http"
	"strings"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"

	"github.com/grafana/sqlds/v5/pool/pooltest"
)

func TestIdentity(t *testing.T) {
	alice := &backend.User{Login: "alice"}
	grafanaID := pooltest.UnsignedJWT(`{"sub":"user:u1","authenticatedBy":"oauth_generic"}`)

	tests := []struct {
		name    string
		user    *backend.User
		headers map[string]string
		want    string
	}{
		{
			name:    "no forwarded credential uses the data source credentials",
			user:    alice,
			headers: map[string]string{"X-Grafana-Id": grafanaID},
			want:    "",
		},
		{
			name:    "grafana ID claims name the user",
			user:    alice,
			headers: map[string]string{"Authorization": "Bearer t1", "X-Grafana-Id": grafanaID},
			want:    hashed("id", "oauth_generic\nuser:u1"),
		},
		{
			name:    "ID token alone is a credential",
			headers: map[string]string{"X-Id-Token": "id1", "X-Grafana-Id": grafanaID},
			want:    hashed("id", "oauth_generic\nuser:u1"),
		},
		{
			name:    "login when the grafana ID is malformed",
			user:    alice,
			headers: map[string]string{"Authorization": "Bearer t1", "X-Grafana-Id": "not-a-jwt"},
			want:    hashed("login", "alice"),
		},
		{
			name:    "login when the grafana ID has no subject",
			user:    alice,
			headers: map[string]string{"Authorization": "Bearer t1", "X-Grafana-Id": pooltest.UnsignedJWT(`{"authenticatedBy":"oauth_generic"}`)},
			want:    hashed("login", "alice"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			for k, v := range tc.headers {
				h.Set(k, v)
			}
			got := Identity(tc.user, h)
			if got != tc.want {
				t.Errorf("Identity() = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, "user:u1") || strings.Contains(got, "alice") {
				t.Errorf("Identity() = %q holds the claim in plain text", got)
			}
		})
	}
}

func TestIdentityFailsClosedWithoutAUserClaim(t *testing.T) {
	identity := func(token string) string {
		h := http.Header{}
		h.Set("Authorization", token)
		return Identity(&backend.User{}, h)
	}

	first, again, other := identity("Bearer t1"), identity("Bearer t1"), identity("Bearer t2")
	if !strings.HasPrefix(first, "token:") {
		t.Fatalf("Identity() = %q, want a token identity", first)
	}
	if first != again {
		t.Errorf("Identity() for one token = %q and %q, want the same", first, again)
	}
	if first == other {
		t.Errorf("Identity() for two tokens = %q for both, want different", first)
	}
}

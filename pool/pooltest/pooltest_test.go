package pooltest

import (
	"context"
	"encoding/base64"
	"maps"
	"strings"
	"testing"
)

func TestCountOpens(t *testing.T) {
	open, opens := CountOpens(func(_ context.Context, key string) (string, error) { return "pool-" + key, nil })
	for _, key := range []string{"alice", "bob", "alice"} {
		if got, _ := open(context.Background(), key); got != "pool-"+key {
			t.Fatalf("open(%q) = %q, want the result of the wrapped open", key, got)
		}
	}
	if want := map[string]int{"alice": 2, "bob": 1}; !maps.Equal(opens(), want) {
		t.Errorf("opens = %v, want %v", opens(), want)
	}
}

func TestUnsignedJWT(t *testing.T) {
	parts := strings.Split(UnsignedJWT(`{"sub":"user:alice"}`), ".")
	if len(parts) != 3 {
		t.Fatalf("got %d parts, want 3", len(parts))
	}
	if claims, err := base64.RawURLEncoding.DecodeString(parts[1]); err != nil || string(claims) != `{"sub":"user:alice"}` {
		t.Errorf("claims = %q, %v", claims, err)
	}
}

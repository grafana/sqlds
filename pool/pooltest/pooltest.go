// Package pooltest has helpers for tests of code that uses package pool.
package pooltest

import (
	"context"
	"encoding/base64"
	"maps"
	"sync"
)

// CountOpens wraps open so that each call counts toward its key, and returns the wrapped func and a
// func that reports the counts.
func CountOpens[K comparable, C any](open func(context.Context, K) (C, error)) (func(context.Context, K) (C, error), func() map[K]int) {
	var mu sync.Mutex
	opens := map[K]int{}
	counted := func(ctx context.Context, key K) (C, error) {
		mu.Lock()
		opens[key]++
		mu.Unlock()
		return open(ctx, key)
	}
	return counted, func() map[K]int {
		mu.Lock()
		defer mu.Unlock()
		return maps.Clone(opens)
	}
}

// UnsignedJWT returns a JWT that carries the JSON claims and no valid signature.
func UnsignedJWT(claims string) string {
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"none"}`)) + "." + enc.EncodeToString([]byte(claims)) + ".sig"
}

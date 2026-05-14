package middleware

import (
	"encoding/json"
	"sync"
	"time"

	contractshttp "github.com/goravel/framework/contracts/http"
)

type idempotencyEntry struct {
	status int
	body   []byte
	at     time.Time
}

var (
	idempotencyStore = make(map[string]*idempotencyEntry)
	idempotencyMu    sync.RWMutex
)

// Idempotency middleware checks Idempotency-Key header.
// If the key was seen before, returns the cached response.
func Idempotency() contractshttp.Middleware {
	return func(ctx contractshttp.Context) {
		key := ctx.Request().Header("Idempotency-Key")
		if key == "" {
			ctx.Request().Next()
			return
		}

		idempotencyMu.RLock()
		entry, exists := idempotencyStore[key]
		idempotencyMu.RUnlock()

		if exists {
			ctx.Response().Header("X-Idempotent-Replayed", "true")
			var body any
			json.Unmarshal(entry.body, &body)
			ctx.Request().AbortWithStatusJson(entry.status, body)
			return
		}

		ctx.Request().Next()
	}
}

// StoreIdempotencyResult stores the result for a given key (call after response)
func StoreIdempotencyResult(key string, status int, body any) {
	if key == "" {
		return
	}
	data, _ := json.Marshal(body)
	idempotencyMu.Lock()
	idempotencyStore[key] = &idempotencyEntry{status: status, body: data, at: time.Now()}
	idempotencyMu.Unlock()
}

func init() {
	// Cleanup old entries every 10 minutes
	go func() {
		for {
			time.Sleep(10 * time.Minute)
			idempotencyMu.Lock()
			cutoff := time.Now().Add(-24 * time.Hour)
			for k, v := range idempotencyStore {
				if v.at.Before(cutoff) {
					delete(idempotencyStore, k)
				}
			}
			idempotencyMu.Unlock()
		}
	}()
}

package middleware

import (
	"encoding/json"
	"os"
	"strconv"
	"sync"
	"time"

	contractshttp "github.com/goravel/framework/contracts/http"
)

type idempotencyEntry struct {
	status int
	body   []byte
	at     time.Time
}

// idempotencyMaxEntries caps the in-memory store to bound memory usage.
// Override with IDEMPOTENCY_MAX_ENTRIES env var.
var idempotencyMaxEntries = func() int {
	if v := os.Getenv("IDEMPOTENCY_MAX_ENTRIES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 10000
}()

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
	if len(idempotencyStore) > idempotencyMaxEntries {
		evictOldestLocked(len(idempotencyStore) - idempotencyMaxEntries)
	}
	idempotencyMu.Unlock()
}

// evictOldestLocked removes the n oldest entries. Caller must hold idempotencyMu.
func evictOldestLocked(n int) {
	if n <= 0 {
		return
	}
	type kv struct {
		key string
		at  time.Time
	}
	all := make([]kv, 0, len(idempotencyStore))
	for k, v := range idempotencyStore {
		all = append(all, kv{k, v.at})
	}
	// Partial sort would be faster, but n is typically small (drift above cap).
	// A full sort keeps the code simple and correctness obvious.
	for i := 0; i < n && i < len(all); i++ {
		oldestIdx := i
		for j := i + 1; j < len(all); j++ {
			if all[j].at.Before(all[oldestIdx].at) {
				oldestIdx = j
			}
		}
		all[i], all[oldestIdx] = all[oldestIdx], all[i]
		delete(idempotencyStore, all[i].key)
	}
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

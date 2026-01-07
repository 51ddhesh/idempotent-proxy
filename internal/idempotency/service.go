package idempotency

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/redis/go-redis/v9"
	"io"
	"log"
	"net/http"
	"time"
)

const (
	LockTTL      = 30 * time.Second
	CacheTTL     = 24 * time.Hour
	WatchdogTick = 10 * time.Second
)

// Lua scripts

// check for existence and sets lock in one atomic step
const luaLock = `
local val = redis.call("GET", KEYS[1])
if val then
	return val -- hit
end
-- key missing, acquire lock
redis.call("SET", KEYS[1], ARGV[1], "EX", ARGV[2])
return nil
`

// only extend TTL if value is "IN_PROGRESS"
// this prevents overwriting a 24H cache with 30s lock
const luaExtend = `
local val = redis.call("GET", KEYS[1])
if val == ARGV[1] then
	redis.call("EXPIRE", KEYS[1], ARGV[2])	
	return 1
end
return 0
`

type CachedResponse struct {
	StatusCode int         `json:"status_code"`
	Headers    http.Header `json:"headers"`
	Body       []byte      `json:"body"`
}

type Service struct {
	rdb *redis.Client
}

func NewService(rdb *redis.Client) *Service {
	return &Service{rdb: rdb}
}

// Middleware
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Idempotency-Key")
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}

		redisKey := "idem:" + key

		ctx := r.Context()

		// atomic check and lock
		res, err := s.rdb.Eval(ctx, luaLock, []string{redisKey}, "IN_PROGRESS", int(LockTTL.Seconds())).Result()

		if err != nil && err != redis.Nil {
			log.Printf("[SERVICE]: Redis Error: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		if res != nil {
			val := res.(string)
			if val == "IN_PROGRESS" {
				w.WriteHeader(http.StatusConflict)
				w.Write([]byte("[PROXY]: 409 Conflict: Request in progress\n"))
				return
			}
			s.serveCache(w, val)
			return
		}

		wdCtx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go s.startWatchdog(wdCtx, redisKey)

		next.ServeHTTP(w, r)
	})
}

func (s *Service) ResponseHook(resp *http.Response) error {
	key := resp.Request.Header.Get("X-Idempotency-Key")
	if key == "" {
		return nil
	}

	if resp.StatusCode >= 500 {
		log.Printf("[SERIVE::IDEMPOTENCY]: Backend 500. Removing lock for %s\n", key)
		s.rdb.Del(context.Background(), "idem:"+key)
		return nil
	}

	bodyBytes, err := io.ReadAll(resp.Body)

	if err != nil {
		return err
	}

	resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	cached := CachedResponse{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
		Body:       bodyBytes,
	}

	jsonData, err := json.Marshal(cached)

	if err != nil {
		return err
	}

	s.rdb.Set(context.Background(), "idem:"+key, jsonData, CacheTTL)
	log.Printf("[SERVICE::IDEMPOTENCY]: Saved response for %s", key)

	return nil
}

func (s *Service) serveCache(w http.ResponseWriter, val string) {
	log.Println("[SERVICE::IDEMPOTENCY]: Cache Hit")
	var data CachedResponse

	if err := json.Unmarshal([]byte(val), &data); err != nil {
		http.Error(w, "Cache Corruption", http.StatusInternalServerError)
		return
	}

	for k, v := range data.Headers {
		for _, h := range v {
			w.Header().Add(k, h)
		}
	}

	w.WriteHeader(data.StatusCode)
	w.Write(data.Body)
}

func (s *Service) startWatchdog(ctx context.Context, key string) {
	ticker := time.NewTicker(WatchdogTick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			err := s.rdb.Eval(context.Background(), luaExtend, []string{key}, "IN_PROGRESS", int(LockTTL.Seconds())).Err()
			if err != nil {
				log.Printf("[SERVICE::WATCHDOG]: Error extending the lock: %v\n", err)
			} else {
				log.Printf("[SERVICE::WATCHDOG]: Extending the lock for %s\n", key)
			}
		}
	}
}

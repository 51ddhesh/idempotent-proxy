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

		val, err := s.rdb.Get(ctx, redisKey).Result()

		if err == nil {
			if val == "IN_PROGRESS" {
				w.WriteHeader(http.StatusConflict)
				w.Write([]byte("[PROXY]: 409 Conflict: Request in progress\n"))
				return
			}
			s.serveCache(w, val)
			return
		}

		if err == redis.Nil {
			if !s.acquireLock(ctx, redisKey) {
				w.WriteHeader(http.StatusConflict)
				return
			}

			wdCtx, cancel := context.WithCancel(context.Background())
			defer cancel()

			go s.startWatchdog(wdCtx, redisKey)
			next.ServeHTTP(w, r)
			return
		}

		log.Printf("[SERVICE]: Redis Error: %v", err)
		http.Error(w, "[SERIVCE]: Internal Server Error", http.StatusInternalServerError)
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

	jsonData, _ := json.Marshal(cached)

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

func (s *Service) acquireLock(ctx context.Context, key string) bool {
	success, _ := s.rdb.SetNX(ctx, key, "IN_PROGRESS", LockTTL).Result()
	return success
}

func (s *Service) startWatchdog(ctx context.Context, key string) {
	ticker := time.NewTicker(WatchdogTick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			log.Printf("[SERVICE::WATCHDOG]: Extending lock for %s", key)
			s.rdb.Expire(context.Background(), key, LockTTL)
		}
	}
}

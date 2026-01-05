package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/redis/go-redis/v9"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

var rdb *redis.Client

type CachedResponse struct {
	StatusCode int         `json:"status_code"`
	Headers    http.Header `json:"headers"`
	Body       []byte      `json:"body"`
}

func setupRedis() {
	rdb = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       0,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		log.Fatal("[REDIS]: Could not connect")
	}

	fmt.Println("[REDIS]: Connection established successfully")
}

func NewProxy(targetHost string) (*httputil.ReverseProxy, error) {
	url, err := url.Parse(targetHost)
	if err != nil {
		return nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(url)

	/*
		the resp.Body is a stream which can be read only once
		to not send back an empty response, we read the butes into memory and save them
		create a new stream and send it in the response
	*/

	proxy.ModifyResponse = func(resp *http.Response) error {
		key := resp.Request.Header.Get("X-Idempotency-Key")
		if key == "" {
			return nil
		}

		if resp.StatusCode >= 500 {
			log.Printf("[PROXY]: Internal Server Error, deleting the lock for: %s\n", key)
			rdb.Del(context.Background(), "idem:"+key)
			return nil
		}

		bodyBytes, err := io.ReadAll(resp.Body)

		if err != nil {
			return err
		}

		resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		cachedData := CachedResponse{
			StatusCode: resp.StatusCode,
			Headers:    resp.Header,
			Body:       bodyBytes,
		}

		jsonData, err := json.Marshal(cachedData)

		if err != nil {
			log.Printf("[PROXY]: Failed to encode data to JSON: %v\n", err)
			return nil
		}

		ctx := context.Background()
		rdb.Set(ctx, "idem:"+key, jsonData, 24*time.Hour)

		log.Printf("[PROXY]: Response cached for key: %s\n", key)
		return nil
	}

	return proxy, nil
}

func main() {
	setupRedis()

	backendURL := "http://localhost:8081/"

	proxy, err := NewProxy(backendURL)

	if err != nil {
		log.Fatal("[PROXY]: Could not init proxy: ", err)
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		idempotencyKey := r.Header.Get("X-Idempotency-Key")

		if idempotencyKey == "" {
			log.Printf("[PROXY]: Passthrough (No Key) -> %s\n", r.URL.Path)
			proxy.ServeHTTP(w, r)
			return
		}

		log.Printf("[PROXY]: Idempotency Key found: %s\n", idempotencyKey)
		redisKey := "idem:" + idempotencyKey
		ctx := context.Background()

		val, err := rdb.Get(ctx, redisKey).Result()

		if err == nil {
			if val == "IN_PROGRESS" {
				w.WriteHeader(http.StatusConflict)
				w.Write([]byte("409 Conflict: Request is currently processing"))
				return
			}

			log.Printf("[PROXY]: Cache Hit. Serving saved response for: %s\n", idempotencyKey)

			var cachedResp CachedResponse
			if err := json.Unmarshal([]byte(val), &cachedResp); err != nil {
				log.Printf("[ERROR]: Failed to unmarshal: %v\n", err)
				http.Error(w, "CRITICAL: Cache Corruption", http.StatusInternalServerError)
				return
			}

			for k, v := range cachedResp.Headers {
				for _, h := range v {
					w.Header().Add(k, h)
				}
			}
			w.WriteHeader(cachedResp.StatusCode)
			w.Write(cachedResp.Body)
			return
		}

		if err == redis.Nil {
			success, err := rdb.SetNX(ctx, redisKey, "IN_PROGRESS", 30*time.Second).Result()

			if err != nil {
				http.Error(w, "[PROXY::REDIS]: Redis Error", http.StatusInternalServerError)
				return
			}

			// Race condition
			if !success {
				w.WriteHeader(http.StatusConflict)
				return
			}

			log.Printf("[PROXY]: Lock acquired. Forwarding to backend\n")

			wdCtx, wdCancel := context.WithCancel(context.Background())
			defer wdCancel()

			go func() {
				ticker := time.NewTicker(10 * time.Second)
				defer ticker.Stop()

				for {
					select {
					case <-wdCtx.Done():
						log.Printf("[PROXY::WATCHDOG]: Request Done. Stopping watchdog for %s\n", idempotencyKey)
						return

					case <-ticker.C:
						log.Printf("[PROXY::WATCHDOG]: Extending lock for %s\n", idempotencyKey)
						rdb.Expire(context.Background(), redisKey, 30*time.Second)
					}
				}
			}()

			proxy.ServeHTTP(w, r)
			return
		}

		// Redis Error (Connection died, etc)
		log.Printf("[PROXY]: Redis connection error: %v\n", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	})

	log.Println("Idempotent Proxy running on: http://localhost:8080/")

	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}

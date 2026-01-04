package main

import (
	"context"
	"fmt"
	"github.com/redis/go-redis/v9"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

var rdb *redis.Client

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

		success, err := rdb.SetNX(ctx, redisKey, "IN_PROGRESS", 30*time.Second).Result()

		if err != nil {
			log.Printf("[PROXY]: Redis Error: %v\n", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		if !success {
			log.Printf("[PROXY]: Duplicate request blocked for key: %s\n", idempotencyKey)
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte("409 Conflict: Request already in progress or completed\n"))
			return
		}

		log.Printf("[PROXY]: Lock acquired. Forwarding to backend\n")

		// TODO: Capture response and save it
		proxy.ServeHTTP(w, r)
	})

	log.Println("Idempotent Proxy running on: http://localhost:8080/")

	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}

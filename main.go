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
		log.Printf("[PROXY]: Intercepted request to: %s\n", r.URL.Path)

		proxy.ServeHTTP(w, r)
	})

	log.Println("Idempotent Proxy running on: http://localhost:8080/")

	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}

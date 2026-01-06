package main

import (
	"github.com/51ddhesh/idempotent-proxy/internal/idempotency"
	"github.com/51ddhesh/idempotent-proxy/internal/store"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
)

func main() {
	rdb, err := store.NewRedisClient("localhost:6379")

	if err != nil {
		log.Fatal(err)
	}

	service := idempotency.NewService(rdb)
	targetURL, _ := url.Parse("http://localhost:8081")

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.ModifyResponse = service.ResponseHook

	handler := service.Middleware(proxy)

	log.Println("[PROXY]: Running on Port :8080")
	if err := http.ListenAndServe(":8080", handler); err != nil {
		log.Fatal(err)
	}
}

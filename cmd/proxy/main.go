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
	targetURL, _ := url.Parse("http://localhost:8082")

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.Transport = &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
	}
	proxy.ModifyResponse = service.ResponseHook

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		log.Printf("[PROXY]: HTTP/Network Error: %v\n", e)
		key := r.Header.Get("X-Idempotency-Key")

		if key != "" {
			log.Printf("[PROXY]: Unlocking %s due to network failure\n", key)
			rdb.Del(r.Context(), "idem:"+key)
		}

		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("Bad Gateway: Network Failure\n"))
	}

	handler := service.Middleware(proxy)

	log.Println("[PROXY]: Backend running on Port :8080")
	if err := http.ListenAndServe(":8080", handler); err != nil {
		log.Fatal(err)
	}
}

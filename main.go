package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
)

func NewProxy(targetHost string) (*httputil.ReverseProxy, error) {
	url, err := url.Parse(targetHost)
	if err != nil {
		return nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(url)
	return proxy, nil
}

func main() {
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

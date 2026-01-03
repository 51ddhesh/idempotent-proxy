package main

import (
	"fmt"
	"log"
	"net/http"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("[BACKEND] Received request: %s %s\n", r.Method, r.URL.Path)

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Request processed successfully\n"))
	})

	fmt.Println("BACKEND: Running on localhost:8081")
	log.Fatal(http.ListenAndServe(":8081", nil))
}

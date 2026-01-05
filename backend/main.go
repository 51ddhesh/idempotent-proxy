package main

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("[BACKEND]: Received request: %s %s\n", r.Method, r.URL.Path)

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[BACKEND]: Request processed successfully\n"))
	})

	http.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("[BACKEND]: Slow request started (45 Seconds)\n")
		time.Sleep(45 * time.Second)
		fmt.Printf("[BACKEND]: Slow request finished\n")

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[BACKEND]: Slow request processed successfully\n"))
	})

	fmt.Println("[BACKEND]: Running on localhost:8081")
	log.Fatal(http.ListenAndServe(":8081", nil))
}

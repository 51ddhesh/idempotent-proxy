package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

var (
	successCount  int64
	conflictCount int64
	failCount     int64
	printOnce     sync.Once
)

func main() {
	url := flag.String("url", "http://localhost:8080", "Target Proxy URL")
	requests := flag.Int("n", 100, "Total number of requests")
	concurrency := flag.Int("c", 50, "Number of concurrent requests")
	mode := flag.String("mode", "throughput", "Mode: 'throughput' (unique keys) or 'contention' (same key)")
	flag.Parse()

	fmt.Printf("IDEMPROXY STRESS TEST\n\n")
	fmt.Printf("Target:             %s\n", *url)
	fmt.Printf("Mode:               %s\n", *mode)
	fmt.Printf("Reqs:               %d\n", *requests)
	fmt.Printf("Concurrent Workers: %d\n", *concurrency)
	fmt.Printf("-----------------------------------------------------------------------------\n")

	start := time.Now()

	var wg sync.WaitGroup

	jobs := make(chan int, *requests)

	baseKey := fmt.Sprintf("stress-%d", time.Now().UnixNano())

	for w := 0; w < *concurrency; w++ {
		wg.Add(1)
		go worker(w, &wg, jobs, *url, *mode, baseKey)
	}

	for i := 0; i < *requests; i++ {
		jobs <- i
	}

	close(jobs)
	wg.Wait()

	duration := time.Since(start)

	printReport(duration, *requests)
}

func worker(id int, wg *sync.WaitGroup, jobs <-chan int, baseURL string, mode string, baseKey string) {
	defer wg.Done()

	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
		},
		Timeout: 60 * time.Second,
	}

	for j := range jobs {
		var key string
		var targetURL string

		if mode == "contention" {
			// Hit /slow
			// 1 worker gets the lock
			// 99 others get 409
			key = baseKey
			targetURL = baseURL + "/slow"
		} else {
			// hit the root /
			// everyone hits a unique key
			// 100 workers get 200 OK

			key = fmt.Sprintf("%s-%d-%d", baseKey, id, j)
			targetURL = baseURL + "/"
		}

		req, _ := http.NewRequest("GET", targetURL, nil)
		req.Header.Set("X-Idempotency-Key", key)

		resp, err := client.Do(req)

		if err != nil {
			atomic.AddInt64(&failCount, 1)
			printOnce.Do(func() {
				fmt.Printf("\n[STRESS::DEBUG]: NETWORK ERROR: %v\n", err)
			})
			continue
		}

		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		switch resp.StatusCode {
		case http.StatusOK:
			atomic.AddInt64(&successCount, 1)
		case http.StatusConflict:
			atomic.AddInt64(&conflictCount, 1)
		default:
			atomic.AddInt64(&failCount, 1)
		}
	}
}

func printReport(duration time.Duration, total int) {
	s := atomic.LoadInt64(&successCount)
	c := atomic.LoadInt64(&conflictCount)
	f := atomic.LoadInt64(&failCount)

	rps := float64(total) / duration.Seconds()

	fmt.Printf("\n\n\nRESULTS\n")
	fmt.Printf("----------------------------------\n")
	fmt.Printf("- Time Taken:      %v\n", duration)
	fmt.Printf("- Throughput:      %.2freq/s\n", rps)
	fmt.Printf("- SUCESS (200 OK): %d\n", s)
	fmt.Printf("- CONFLICT (409):  %d\n", c)
	fmt.Printf("- FAILURE (5xx):   %d\n", f)

	if f > 0 {
		fmt.Println("[STRESS]: WARNING: Failures Detected")
	} else if c > 0 {
		fmt.Println("[STRESS]: WARNING: Conflicts Expected")
	}

	fmt.Printf("\033[0;32m[STRESS]: RUN COMPLETED \n\033[0m")
}

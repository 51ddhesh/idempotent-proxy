# IdemProxy: Distributed Idempotency Sidecar

**A high-performance Reverse Proxy written in Go that guarantees "Exactly-Once" execution for API requests.**

![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)
![Redis](https://img.shields.io/badge/Redis-Distributed_Lock-DC382D?style=flat&logo=redis)
![Docker](https://img.shields.io/badge/Docker-Containerized-2496ED?style=flat&logo=docker)


## The Problem (Why this exists)
In distributed systems (like Payments), network failures are inevitable.
1.  **Scenario:** A client requests a $50 charge.
2.  **Failure:** The server processes the charge, but the internet cuts out before the response reaches the client.
3.  **Retry:** The client (or user) clicks "Pay" again.
4.  **Result:** **Double Charge.**

**IdemProxy** sits between the Client and the Backend to prevent this. It enforces **Idempotency** using Distributed Locking and Response Caching.

## Architecture & Logic
### The Flow
1.  **Interception:** The Proxy intercepts every HTTP request.
2.  **Identification:** It checks for an `X-Idempotency-Key` header.
3.  **Distributed Lock (Redis `SETNX`):**
    *   **If Key exists (Completed):** Return the cached JSON response immediately. **(Circuit Breaker)**.
    *   **If Key exists (In Progress):** Return `409 Conflict` to prevent race conditions.
    *   **If New:** Acquire a lock with a TTL (Time-To-Live) and forward the request to the backend.
4.  **The Watchdog (Heartbeat):**
    *   A background Goroutine spins up to "ping" Redis every 10 seconds, extending the Lock TTL.
    *   *Why?* This ensures that if the Backend takes 45s to process a request, the 30s lock doesn't expire, preventing a second request from slipping through.
5.  **Response Caching:**
    *   When the Backend responds, the Proxy captures the body, saves it to Redis (24h retention), and serves it to the client.

## Tech Stack

*   **Language:** Go (Golang) - chosen for high concurrency (`goroutines`) and standard library network capabilities.
*   **Store:** Redis - used for atomic locking (`SETNX`) and fast K/V caching.
*   **Infrastructure:** Docker & Docker Compose.

## Getting Started

### Prerequisites
*   Go 1.21+
*   Docker & Docker Compose

### 1. Start Infrastructure (Redis + Toxiproxy)
```bash
docker-compose up -d
```

### 2. Run the Mock Backend
This mimics a payment service. It has a `/slow` endpoint to simulate long-running jobs.
```bash
go run backend/main.go
# Runs on localhost:8081
```

### 3. Run the Proxy
```bash
go run cmd/proxy/main.go
# Runs on localhost:8080 (Proxies traffic to :8081 via :8082)
```

## Testing 
### 1. The "Double Charge" Protection
Send a request with a key, then immediately send it again.
```bash
# Request 1 (Success)
curl -v -H "X-Idempotency-Key: charge_123" http://localhost:8080/

# Request 2 (Replay) - Returns Cached Response, Backend is NOT touched
curl -v -H "X-Idempotency-Key: charge_123" http://localhost:8080/
```

### 2. The "Slow Job" Watchdog
This endpoint sleeps for 45s. The Lock TTL is only 30s. The Watchdog keeps the lock alive.
```bash
# Terminal A: Start a slow job
curl -v -H "X-Idempotency-Key: slow_job_1" http://localhost:8080/slow

# Terminal B (at T+35s): Try to start it again
curl -v -H "X-Idempotency-Key: slow_job_1" http://localhost:8080/slow
# Result: 409 Conflict (Lock successfully extended)
```


## Design Decisions & Trade-offs

1.  **Atomic Locking:** Used Redis `SETNX` instead of standard `SET` to prevent race conditions where two requests check for existence at the exact same millisecond.
2.  **The Watchdog Pattern:** Implemented a `time.Ticker` inside a Goroutine to handle lock extension. This is preferable to setting a massive TTL (which creates zombie locks if the server crashes).
3.  **Fail-Open vs Fail-Closed:** Currently configured to `Fail-Closed` on Redis errors (return 500). In a payment system, it is safer to fail than to process a payment twice.

---

## Future Improvements
*   **Redis Lua Scripts:** To make the "Check and Set" operation purely atomic in a single network round-trip.
*   **Request Hash Validation:** Ensure the payload of the second request matches the first request (preventing key reuse for different operations).
*   **Metrics:** Add Prometheus metrics for Lock Contentions and Cache Hits.
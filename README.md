# IdemProxy: Distributed Idempotency Sidecar

A high-performance Reverse Proxy written in Go that guarantees "Exactly-Once" execution for API requests.

![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go)
![Redis](https://img.shields.io/badge/Redis-Distributed_Lock-DC382D?style=flat&logo=redis)
![Docker](https://img.shields.io/badge/Docker-Containerized-2496ED?style=flat&logo=docker)

## The Problem
In distributed systems (like Payments), network failures are inevitable.
1.  **Scenario:** A client requests a $50 charge.
2.  **Failure:** The server processes the charge, but the internet cuts out before the response reaches the client.
3.  **Retry:** The client clicks "Pay" again.
4.  **Result:** Double Charge.

**IdemProxy** sits between the Client and the Backend to prevent this. It enforces Idempotency using Distributed Locking and Response Caching.

## Architecture & Logic

### The Flow
1.  **Interception:** The Proxy intercepts every HTTP request checking for an `X-Idempotency-Key` header.
2.  **Distributed Lock (Redis SETNX):**
    *   **Cache Hit:** If the key exists and is `COMPLETED`, return the saved JSON response immediately. The backend is never touched.
    *   **In Progress:** If the key exists but is `IN_PROGRESS`, return `409 Conflict` to prevent race conditions.
    *   **New Request:** Acquire a lock with a TTL (Time-To-Live) and forward the request.
3.  **The Watchdog (Heartbeat):**
    *   A background Goroutine "pings" Redis every 10 seconds to extend the Lock TTL.
    *   *Why?* Ensures that if the Backend takes 45s to process a request, the 30s lock doesn't expire, preventing a second request from slipping through.
4.  **Fault Tolerance:**
    *   If the network cuts out (`EOF`), the Proxy detects the failure and releases the lock immediately, allowing the client to retry safely.

## Getting Started

### Prerequisites
*   Go 1.24.1+
*   Docker & Docker Compose
*   Make

### Automated Testing (Recommended)
This project includes a comprehensive integration test suite that spins up the infrastructure, configures network proxies, and runs resilience tests.

**Run the full suite (Infrastructure + Tests):**
```bash
make test
```

**Run tests only (Fast Mode):**
Use this if Docker is already running and you are iterating on the Go code.
```bash
make test-only
```

**What the test suite does:**
1.  **Environment Setup:** Configures Docker services and Toxiproxy routes.
2.  **Scenario A (Idempotency):** Validates atomic locking and response caching.
3.  **Scenario B (Watchdog):** Validates lock extension for long-running jobs (45s+).
4.  **Scenario C (Chaos):** Injects network faults (EOF) to ensure locks are released safely ("Fail Open").

### Manual Operation
If you prefer to run components individually for development:

1.  **Start Infrastructure:**
    ```bash
    make up
    ```
2.  **Run Proxy:**
    ```bash
    go run cmd/proxy/main.go
    ```
3.  **Stop Infrastructure:**
    ```bash
    make down
    ```

## Manual Verification Scenarios

You can verify the system's resilience manually using `curl`.

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

### 3. Network Failure (Toxiproxy)
Simulate a "Cable Cut" where the connection drops halfway through the response.

**A. Add the Toxic:**
```bash
curl -X POST -d '{"name":"cut_cable","type":"limit_data","attributes":{"bytes":10}}' \
http://localhost:8474/proxies/backend_pipe/toxics
```

**B. Send Request:**
```bash
curl -v -H "X-Idempotency-Key: fail_test" http://localhost:8080/
# Result: 502 Bad Gateway (Proxy detects failure and DELETEs the lock)
```

**C. Retry (Success):**
Running the request again will *not* return `409 Conflict`, but will attempt to process again. This proves the system correctly handles partial failures.

## Design Decisions

1.  **Atomic Locking:** Used Redis `SETNX` to prevent race conditions where two requests check for existence at the exact same millisecond.
2.  **The Watchdog Pattern:** Implemented a `time.Ticker` inside a Goroutine. This is preferable to setting a massive TTL (which creates zombie locks if the server crashes).
3.  **Fail-Open vs Fail-Closed:** Currently configured to `Fail-Closed` on Redis errors (return 500). In a payment system, it is safer to fail than to process a payment twice.

## Future Improvements
*   **Redis Lua Scripts:** To make the "Check and Set" operation purely atomic in a single network round-trip.
*   **Request Hash Validation:** Ensure the payload of the second request matches the first request (preventing key reuse for different operations).
*   **Metrics:** Add Prometheus metrics for Lock Contentions and Cache Hits.

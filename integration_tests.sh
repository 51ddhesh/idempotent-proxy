#!/bin/bash
# IdemProxy Integration Test Suite
# This script performs end-to-end testing of the IdemProxy service.
# It handles infrastructure setup, execution of test scenarios (Happy Path, 
# Long-Running Jobs, Chaos Engineering), and teardown.
set -euo pipefail # Strict mode: Fail on error, unset vars, or pipe failures

PROXY_HOST="http://localhost:8080"
TOXI_HOST="http://localhost:8474"
LOG_FILE="service.log"
TEST_ID=$(date +%s) # Unique ID to ensure clean Redis state per run

GREEN='\033[0;32m'
RED='\033[0;31m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_pass() {
    echo -e "${GREEN}[PASS]${NC} $1"
}

log_fail() {
    echo -e "${RED}[FAIL]${NC} $1"
    echo -e "${YELLOW}--- Tail of Service Logs ---${NC}"
    tail -n 10 "$LOG_FILE"
    cleanup
    exit 1
}

wait_for_service() {
    local url=$1
    local max_retries=10
    local count=0
    
    log_info "Waiting for service at $url..."
    until curl -s "$url" > /dev/null; do
        sleep 1
        count=$((count+1))
        if [ $count -ge $max_retries ]; then
            log_fail "Service failed to start after $max_retries seconds."
        fi
    done
}

cleanup() {
    log_info "Performing teardown..."
    if [ -n "${PROXY_PID:-}" ]; then
        kill "$PROXY_PID" 2>/dev/null || true
    fi
    rm -f "$LOG_FILE"
}

trap cleanup EXIT

log_info "Initializing Test Environment..."

# PRE-FLIGHT CHECK: Is Toxiproxy (Docker) running?
if ! curl -s "$TOXI_HOST/proxies" > /dev/null; then
    log_fail "Toxiproxy is not reachable at $TOXI_HOST. Run 'make up' first."
fi

# clear port 8080 if occupied
fuser -k 8080/tcp > /dev/null 2>&1 || true

# Reset Toxiproxy State
curl -s -X DELETE "$TOXI_HOST/proxies/backend_pipe" > /dev/null || true
curl -s -X POST -d '{
  "name": "backend_pipe",
  "listen": "0.0.0.0:8082",
  "upstream": "backend:8081",
  "enabled": true
}' "$TOXI_HOST/proxies" > /dev/null

log_info "Network proxy configured."

# Start the Go Proxy in background
go run cmd/proxy/main.go > "$LOG_FILE" 2>&1 &
PROXY_PID=$!

wait_for_service "$PROXY_HOST"

log_info "Waiting for Backend to be reachable via Proxy..."
MAX_RETRIES=30
COUNT=0
WARMUP_KEY="healthcheck-init"

while true; do
    # Try to hit the backend via the proxy
    HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Idempotency-Key: $WARMUP_KEY" "$PROXY_HOST/")
    
    # If we get 200, the pipe is open
    if [ "$HTTP_CODE" -eq 200 ]; then
        log_info "Backend is online and reachable."
        break
    fi

    # If we get 502 (Bad Gateway), the backend is still booting. Wait.
    count=$((COUNT+1))
    if [ $COUNT -ge $MAX_RETRIES ]; then
        log_fail "Backend failed to become reachable after $MAX_RETRIES seconds."
    fi
    
    echo -n "."
    sleep 1
done
echo ""

# --- Scenario A: Standard Idempotency ---
echo ""
log_info "Starting Scenario A: Standard Idempotency"
KEY_A="std-$TEST_ID"

# Request 1
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Idempotency-Key: $KEY_A" "$PROXY_HOST/")
if [ "$HTTP_CODE" -eq 200 ]; then
    log_pass "First request succeeded (Lock Acquired & Processed)"
else
    log_fail "First request failed. Got $HTTP_CODE"
fi

# Request 2 (Replay)
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Idempotency-Key: $KEY_A" "$PROXY_HOST/")
if [ "$HTTP_CODE" -eq 200 ]; then
    log_pass "Second request succeeded (Cache Hit)"
else
    log_fail "Second request failed. Got $HTTP_CODE"
fi


# --- Scenario B: Watchdog (Lock Extension) ---
log_info "Starting Scenario B: Watchdog Verification (Slow Backend)"
KEY_B="slow-$TEST_ID"

log_info "Sending request to /slow (Expect ~45s duration)..."
# Using 'time' to output duration to user
start_time=$SECONDS
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Idempotency-Key: $KEY_B" "$PROXY_HOST/slow")
duration=$(( SECONDS - start_time ))

if [ "$HTTP_CODE" -eq 200 ]; then
    log_pass "Slow request completed successfully in ${duration}s"
else
    log_fail "Slow request failed. Got $HTTP_CODE"
fi


# --- Scenario C: Fault Tolerance (Network Failure) ---
log_info "Starting Scenario C: Chaos Engineering (Network Interruption)"
KEY_C="chaos-$TEST_ID"

# Inject Toxic: Cut connection after 10 bytes
log_info "Injecting Toxic: LimitData (10 bytes)..."
curl -s -X POST -d '{
    "name": "limit_data_early",
    "type": "limit_data", 
    "attributes": {"bytes": 10}
}' "$TOXI_HOST/proxies/backend_pipe/toxics" > /dev/null

# Send Request (Expect Failure)
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Idempotency-Key: $KEY_C" "$PROXY_HOST/")

if [ "$HTTP_CODE" -eq 502 ]; then
    log_pass "Proxy correctly returned 502 Bad Gateway"
else
    log_fail "Expected 502 Bad Gateway, got $HTTP_CODE"
fi

# Verify Internal State (Log Analysis)
if grep -q "Unlocking $KEY_C" "$LOG_FILE"; then
    log_pass "System log confirms lock release on failure"
else
    log_fail "System log does not show lock release!"
fi

# Remove Toxic and Retry
log_info "Removing Toxic..."
curl -s -X DELETE "$TOXI_HOST/proxies/backend_pipe/toxics/limit_data_early" > /dev/null
sleep 1 

HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Idempotency-Key: $KEY_C" "$PROXY_HOST/")
if [ "$HTTP_CODE" -eq 200 ]; then
    log_pass "Retry successful after network recovery"
else
    log_fail "Retry failed. Got $HTTP_CODE"
fi

echo ""
echo -e "${GREEN}=========================================${NC}"
echo -e "${GREEN}  INTEGRATION SUITE PASSED SUCCESSFULLY  ${NC}"
echo -e "${GREEN}=========================================${NC}"
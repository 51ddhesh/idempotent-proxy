#!/bin/bash

# --- CONFIGURATION ---
GREEN='\033[0;32m'
RED='\033[0;31m'
CYAN='\033[0;36m'
YELLOW='\033[1;33m'
NC='\033[0m'

PROXY_HOST="http://localhost:8080"
TOXI_HOST="http://localhost:8474"

# Generate a random ID for this run (Prevents Redis Cache Hits from previous runs)
RUN_ID=$(date +%s)

# --- 1. CLEANUP PREVIOUS MESS (The Zombie Killer) ---
echo -e "${YELLOW}[INIT] Cleaning up port 8080...${NC}"
# Find any process on 8080 and kill it
PID=$(lsof -t -i:8080)
if [ ! -z "$PID" ]; then
    kill -9 $PID
fi
# Remove the log file
rm -f proxy.log

# Ensure we kill the new proxy when this script exits (even if Ctrl+C)
trap "kill 0" EXIT

# --- 2. SETUP TOXIPROXY ---
echo -e "${YELLOW}[INIT] Configuring Toxiproxy...${NC}"
# Delete old pipe
curl -s -X DELETE $TOXI_HOST/proxies/backend_pipe > /dev/null
# Create fresh pipe
curl -s -X POST -d '{
  "name": "backend_pipe",
  "listen": "0.0.0.0:8082",
  "upstream": "backend:8081",
  "enabled": true
}' $TOXI_HOST/proxies > /dev/null

# --- 3. START PROXY ---
echo -e "${YELLOW}[INIT] Starting Go Proxy...${NC}"
go run cmd/proxy/main.go > proxy.log 2>&1 &
PROXY_PID=$!

# Wait for it to be ready
echo -n "Waiting for proxy to boot"
for i in {1..10}; do
    if curl -s http://localhost:8080 > /dev/null; then 
        echo -e " ${GREEN}OK${NC}"
        break
    fi
    echo -n "."
    sleep 1
done

# --- HELPER FUNCTION ---
check_status() {
    local url=$1
    local key=$2
    local expected=$3
    local desc=$4

    CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Idempotency-Key: $key" "$url")
    if [ "$CODE" == "$expected" ]; then
        echo -e "${GREEN}✓ [PASS] $desc ($CODE)${NC}"
    else
        echo -e "${RED}✗ [FAIL] $desc (Expected $expected, Got $CODE)${NC}"
        tail -n 5 proxy.log
        exit 1 # Stop script on failure
    fi
}

# --- TEST 1: NORMAL FLOW ---
echo -e "\n${CYAN}--- TEST 1: Fresh Request & Caching ---${NC}"
KEY="normal-$RUN_ID"
check_status "$PROXY_HOST/" "$KEY" "200" "First Request (Lock & Process)"
check_status "$PROXY_HOST/" "$KEY" "200" "Second Request (Cache Hit)"

# --- TEST 2: WATCHDOG ---
echo -e "\n${CYAN}--- TEST 2: Watchdog (Slow Backend) ---${NC}"
echo -e "${YELLOW}Sending slow request (This MUST take 45s)...${NC}"
KEY="slow-$RUN_ID"
# We use 'time' to prove it actually waited
time check_status "$PROXY_HOST/slow" "$KEY" "200" "Slow Request Finished"

# --- TEST 3: CHAOS ---
echo -e "\n${CYAN}--- TEST 3: Chaos (Network Failure) ---${NC}"
KEY="chaos-$RUN_ID"

# Add named toxic
echo -e "${YELLOW}[CHAOS] Cutting cable...${NC}"
curl -s -X POST -d '{
    "name": "cut_down_middle",
    "type": "limit_data", 
    "attributes": {"bytes": 10}
}' $TOXI_HOST/proxies/backend_pipe/toxics > /dev/null

# Expect Fail
check_status "$PROXY_HOST/" "$KEY" "502" "Request fails (Network Cut)"

# Check Logs for Unlock
if grep -q "Unlocking $KEY" proxy.log; then
    echo -e "${GREEN}✓ [PASS] Log confirmed: Lock released${NC}"
else
    echo -e "${RED}✗ [FAIL] Lock was NOT released!${NC}"
    exit 1
fi

# Remove toxic
echo -e "${YELLOW}[CHAOS] Repairing cable...${NC}"
curl -s -X DELETE $TOXI_HOST/proxies/backend_pipe/toxics/cut_down_middle > /dev/null
sleep 1 # Allow network to settle

# Retry
check_status "$PROXY_HOST/" "$KEY" "200" "Retry succeeds after repair"

echo -e "\n${GREEN}TEST COMPLETED.${NC}"
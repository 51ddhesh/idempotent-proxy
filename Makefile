GREEN := \033[0;32m
YELLOW := \033[1;33m
BLUE := \033[0;34m
NC := \033[0m
TOXI_HOST := http://localhost:8474

.PHONY: up down test test-only clean logs deps setup-toxi stress-throughput stress-contention

deps:
	@echo "${BLUE}[MAKEFILE] Checking dependencies...${NC}"
	@go mod tidy
	@go mod download

up:
	@echo "${YELLOW}[MAKEFILE] Ensuring Docker infrastructure is up...${NC}"
	@docker-compose up -d --build

down:
	@echo "${YELLOW}[MAKEFILE] Stopping Docker infrastructure...${NC}"
	@docker-compose down

setup-toxi:
	@echo "${BLUE}[MAKEFILE] Configuring Toxiproxy Route...${NC}"
	@curl -s -X DELETE $(TOXI_HOST)/proxies/backend_pipe > /dev/null || true
	@curl -s -X POST -d '{"name": "backend_pipe", "listen": "0.0.0.0:8082", "upstream": "backend:8081", "enabled": true}' $(TOXI_HOST)/proxies > /dev/null

test: deps up test-only
test-only: deps
	@echo "${BLUE}[MAKEFILE] Running integration suite...${NC}"
	@chmod +x integration_tests.sh
	@./integration_tests.sh
	@echo "${GREEN}[MAKEFILE] Done.${NC}"

startup-proxy:
	@echo "${BLUE}[MAKEFILE] Starting the proxy...${NC}"
	@go run cmd/proxy/main.go
	@echo "${GREEN}[MAKEFILE] Done.${NC}"

stress-throughput: setup-toxi
	@echo "${BLUE}[MAKEFILE] Running Throughput Test (1000 Req, 50 Workers)...${NC}"
	@go run cmd/stress/main.go -mode=throughput -n 1000 -c 50

stress-contention: setup-toxi
	@echo "${BLUE}[MAKEFILE] Running Contention Test (20 Users, Same Key)...${NC}"
	@go run cmd/stress/main.go -mode=contention -n 20 -c 20

logs:
	@docker-compose logs -f backend

clean:
	@rm -f service.log
	@rm -f backend-bin
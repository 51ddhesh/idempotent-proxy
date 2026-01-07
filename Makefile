GREEN := \033[0;32m
YELLOW := \033[1;33m
BLUE := \033[0;34m
NC := \033[0m

.PHONY: up down test test-only clean logs

up:
	@echo "${YELLOW}[MAKEFILE] Ensuring Docker infrastructure is up...${NC}"
	@docker-compose up -d --build

down:
	@echo "${YELLOW}[MAKEFILE] Stopping Docker infrastructure...${NC}"
	@docker-compose down

# Run Tests (Ensures infrastructure is up first)
# Use this for CI/CD or fresh runs.
test: up test-only

# Run Tests ONLY (Fast Mode)
# Use this if Docker is ALREADY running and you just changed Go code.
test-only:
	@echo "${BLUE}[MAKEFILE] Running integration suite...${NC}"
	@chmod +x integration_tests.sh
	@./integration_tests.sh
	@echo "${GREEN}[MAKEFILE] Done.${NC}"

logs:
	@docker-compose logs -f backend

clean:
	@rm -f service.log
	@rm -f backend-bin
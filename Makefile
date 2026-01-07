GREEN := \033[0;32m
YELLOW := \033[1;33m
BLUE := \033[0;34m
NC := \033[0m

.PHONY: up down test test-only clean logs deps

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

test: deps up test-only

test-only: deps
	@echo "${BLUE}[MAKEFILE] Running integration suite...${NC}"
	@chmod +x integration_tests.sh
	@./integration_tests.sh
	@echo "${GREEN}[MAKEFILE] Done.${NC}"

logs:
	@docker-compose logs -f backend

clean:
	@rm -f service.log
	@rm -f backend-bin

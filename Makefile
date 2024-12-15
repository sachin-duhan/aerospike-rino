.PHONY: build run clean docker-up docker-down docker-logs deps test help kafka-ui load-test aerospike-ui

# Default target
.DEFAULT_GOAL := help

# Docker related variables
DOCKER_COMPOSE=docker-compose

help: ## Show this help message
	@echo 'Usage:'
	@echo '  make <target>'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*##"; printf "\033[36m"} /^[a-zA-Z_-]+:.*?##/ { printf "  %-15s %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

build-api: ## Build the API service
	cd api && go build -o kyc-api

build-consumer: ## Build the consumer service
	cd consumer && go build -o kyc-consumer

build-loadtest: ## Build the load testing tool
	cd loadtest && go build -o kyc-loadtest

build: build-api build-consumer build-loadtest ## Build all services

run-api: ## Run the API service
	cd api && go run main.go

run-consumer: ## Run the consumer service
	cd consumer && go run main.go

clean: ## Clean build files
	rm -f api/kyc-api consumer/kyc-consumer loadtest/kyc-loadtest
	cd api && go clean
	cd consumer && go clean
	cd loadtest && go clean

docker-up: ## Start all docker containers
	$(DOCKER_COMPOSE) up -d --build

docker-down: ## Stop all docker containers
	$(DOCKER_COMPOSE) down -v

docker-logs: ## View docker container logs
	$(DOCKER_COMPOSE) logs -f

docker-logs-api: ## View API service logs
	$(DOCKER_COMPOSE) logs -f api

docker-logs-consumer: ## View consumer service logs
	$(DOCKER_COMPOSE) logs -f consumer

docker-logs-kafka-ui: ## View Kafka UI logs
	$(DOCKER_COMPOSE) logs -f kafka-ui

docker-logs-aerospike: ## View Aerospike logs
	$(DOCKER_COMPOSE) logs -f aerospike

docker-logs-aerospike-browser: ## View Aerospike browser logs
	$(DOCKER_COMPOSE) logs -f aerospike-browser

kafka-ui: ## Open Kafka UI in default browser (macOS only)
	open http://localhost:8081

aerospike-ui: ## Open Aerospike browser in default browser (macOS only)
	open http://localhost:8082

kafka-topic: ## Create Kafka topic
	$(DOCKER_COMPOSE) exec kafka kafka-topics.sh \
		--create \
		--topic kyc_topic \
		--bootstrap-server kafka:9092 \
		--replication-factor 1 \
		--partitions 1

kafka-list-topics: ## List Kafka topics
	$(DOCKER_COMPOSE) exec kafka kafka-topics.sh \
		--list \
		--bootstrap-server kafka:9092

kafka-consume: ## Consume messages from Kafka topic
	$(DOCKER_COMPOSE) exec kafka kafka-console-consumer.sh \
		--bootstrap-server kafka:9092 \
		--topic kyc_topic \
		--from-beginning

deps-api: ## Download API dependencies
	cd api && go mod download && go mod tidy

deps-consumer: ## Download consumer dependencies
	cd consumer && go mod download && go mod tidy

deps-loadtest: ## Download load testing dependencies
	cd loadtest && go mod download && go mod tidy

deps: deps-api deps-consumer deps-loadtest ## Download all dependencies

test-api: ## Run API tests
	cd api && go test -v ./...

test-consumer: ## Run consumer tests
	cd consumer && go test -v ./...

test: test-api test-consumer ## Run all tests

load-test: build-loadtest ## Run load test (50 req/sec for 5 minutes)
	@echo "Starting load test..."
	cd loadtest && ./kyc-loadtest -rate 50 -duration 5m

load-test-heavy: build-loadtest ## Run heavy load test (200 req/sec for 5 minutes)
	@echo "Starting heavy load test..."
	cd loadtest && ./kyc-loadtest -rate 200 -duration 5m

start-all: docker-up ## Start everything (docker containers with both services)
	@echo "Services are starting..."
	@echo "Kafka UI will be available at http://localhost:8081"
	@echo "Aerospike Browser will be available at http://localhost:8082"
	@echo "API will be available at http://localhost:8080"

stop-all: docker-down clean ## Stop everything and clean up
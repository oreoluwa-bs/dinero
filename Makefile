.PHONY: help infra api worker load-test-smoke load-test load-test-spike load-test-e2e reset-db clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

infra: ## Start infrastructure (RabbitMQ, Prometheus, Grafana, Tempo)
	docker compose up -d

api: ## Run the API server
	go run ./cmd/api

worker: ## Run the worker
	go run ./cmd/worker

reset-db: ## Delete SQLite database (run between load tests)
	rm -f data.db

load-test-smoke: ## Run k6 smoke test (1 VU, 10 iters)
	k6 run k6/smoke.js

load-test: ## Run k6 load test (10 VUs, 5 min)
	k6 run k6/load.js

load-test-spike: ## Run k6 spike test (50 VUs burst)
	k6 run k6/spike.js

load-test-e2e: ## Run k6 end-to-end test (polls for completion)
	k6 run k6/e2e.js

clean: ## Stop all infrastructure and remove DB
	docker compose down
	rm -f data.db

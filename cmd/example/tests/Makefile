.PHONY: help scenarios load pull

help: ## Show this help
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

pull: ## Pull k6 image
	podman pull grafana/k6:latest

scenarios: ## Run smoke test (2 min)
	./run-test.sh k6-scenarios.js

load: ## Run load test (9 min)
	./run-test.sh k6-load-test.js
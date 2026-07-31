.PHONY: help dev dev-db dev-backend dev-frontend dev-init stop clean build-backend build-gateway build-frontend test-backend test-box box-cost box-billing-rehearsal test migrate helm-lint helm-render fmt fmt-check hooks

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

dev-db: ## Start PostgreSQL + pgAdmin
	docker compose up -d postgres pgadmin
	@echo "Waiting for postgres..."
	@sleep 3
	@echo "PostgreSQL ready at localhost:5432"
	@echo "pgAdmin at http://localhost:5050 (admin@dada.local / admin)"

dev-init: dev-db ## Initialize dev environment (DB + Git state repo)
	@bash scripts/init-state-repo.sh
	@echo "Dev environment initialized."

dev-backend: ## Run backend API + worker
	@cd backend && go run ./cmd/server

dev-frontend: ## Run Next.js dev server
	@cd frontend && npm run dev

stop: ## Stop docker containers
	docker compose down

clean: ## Remove docker volumes (wipes database!)
	docker compose down -v

build-backend: ## Build backend binary
	@cd backend && go build -o bin/server ./cmd/server

build-gateway: ## Build telemetry gateway binary
	@cd backend && go build -o bin/gateway ./cmd/gateway

build-frontend: ## Build frontend for production
	@cd frontend && npm run build

test-backend: ## Run backend tests
	@cd backend && go test ./...

test-box: ## Run Dada Box tests (ready path, readiness, pool, metric surface, metering)
	@cd backend && go test ./internal/box/... ./internal/metrics/... ./internal/boxcatalog/... ./internal/billing/... -count=1
	@cd backend && go test ./internal/api/ -count=1 -run 'Box|ClassifyBoxMinute|SpendCap|GuestCannot|Guest'

box-cost: ## Print the derived Dada Box unit economics and check the margin
	@scripts/box-unit-cost-check.sh

box-billing-rehearsal: ## Prove an idle box accumulates zero (needs TEST_DATABASE_URL)
	@scripts/box-idle-billing-rehearsal.sh

migrate: ## Apply DB migrations (uses DATABASE_URL or TEST_DATABASE_URL)
	@cd backend && go run ./cmd/migrate

test: test-backend ## Run all tests (backend unit + golden manifests)
	@echo "All tests passed."

fmt-check: ## Same gofmt gate Jenkins runs before any build (Jenkinsfile 'Go format check')
	@unformatted=$$(gofmt -l backend build-agent gitops-agent mcp-server portainer-agent tools/dbmove); \
	if [ -n "$$unformatted" ]; then \
	  echo "gofmt violations in:"; echo "$$unformatted"; \
	  echo "Fix with: make fmt"; exit 1; \
	fi; \
	echo "all Go modules gofmt-clean"

fmt: ## Rewrite all Go modules with gofmt
	@gofmt -w backend build-agent gitops-agent mcp-server portainer-agent tools/dbmove
	@echo "gofmt -w applied"

hooks: ## Point git at .githooks (feat/* ban + pre-push gofmt gate)
	@git config core.hooksPath .githooks
	@echo "core.hooksPath = $$(git config core.hooksPath)"

helm-lint: ## Lint the Helm chart
	helm lint helm/dada-cloud-console

helm-render: ## Render Helm chart to stdout (smoke check)
	helm template dada-cloud-console helm/dada-cloud-console \
	  --namespace devops-tools \
	  --set backend.image.tag=dev \
	  --set frontend.image.tag=dev

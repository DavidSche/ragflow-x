# ragflow-x Makefile — common development commands
# Run `make help` to see available targets.

.PHONY: help build build-server build-web dev dev-web tidy check \
        start start-local stop status restart \
        test test-v test-p0 test-contract test-race test-coverage test-frontend-coverage test-frontend-coverage-report \
        test-integration test-pg-contract test-operational-contracts test-warning-contracts test-e2e test-quality-report test-changed-coverage test-coverage-trend test-query-plan-trend test-flake-report test-governance-report check-nightly check-release lint vet typecheck clean \
        docker-build docker-up docker-down docker-log docker-ps docker-config

# ─── Platform detection ───────────────────────────────────────────────
ifeq ($(OS),Windows_NT)
  PS     := powershell -NoProfile -ExecutionPolicy Bypass -File
  SCRIPT_EXT := .ps1
else
  SCRIPT_EXT := .sh
endif

SCRIPTS := scripts

FLAKE_INPUTS ?= -input test-results/go-test.json -input test-results/vitest.json -input test-results/playwright.json
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null)

# ─── Default target ──────────────────────────────────────────────────
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

# ─── Build ───────────────────────────────────────────────────────────
build: ## Build all Go packages
	go build ./...

build-server: ## Build server binary
	go build -o bin/ragflow-x ./cmd/server/

build-web: ## Build frontend (tsc + vite)
	cd web && npm run build

# ─── Dev ─────────────────────────────────────────────────────────────
dev: ## Run Go server in debug mode
	go run ./cmd/server/

dev-web: ## Run frontend dev server
	cd web && npm run dev

# ─── Service lifecycle (background daemon) ───────────────────────────
ifeq ($(OS),Windows_NT)
start: ## Start server (default config/config.yaml)
	@$(PS) $(SCRIPTS)/start$(SCRIPT_EXT)

start-local: ## Start server with local sqlite + mock RAGFlow
	@$(PS) $(SCRIPTS)/start$(SCRIPT_EXT) -Local

stop: ## Stop server
	@$(PS) $(SCRIPTS)/stop$(SCRIPT_EXT)

status: ## Show server status
	@$(PS) $(SCRIPTS)/status$(SCRIPT_EXT)
else
start: ## Start server (default config/config.yaml)
	@bash $(SCRIPTS)/start$(SCRIPT_EXT)

start-local: ## Start server with local sqlite + mock RAGFlow
	@bash $(SCRIPTS)/start$(SCRIPT_EXT) -Local

stop: ## Stop server
	@bash $(SCRIPTS)/stop$(SCRIPT_EXT)

status: ## Show server status
	@bash $(SCRIPTS)/status$(SCRIPT_EXT)
endif

restart: stop start ## Restart server

# ─── Test ────────────────────────────────────────────────────────────
test: ## Run all Go unit tests
	go test -count=1 -shuffle=on ./...

test-v: ## Run all Go tests (verbose)
	go test -v -count=1 -shuffle=on ./...

test-integration: ## Run tests with PostgreSQL (set RGX_TEST_POSTGRES_DSN; optional PostgreSQL/MySQL admin DSNs run SQL Tool integration)
	go test -v -count=1 -shuffle=on -tags=integration ./...

test-pg-contract: ## Run internal/db PG contract tests against a disposable rgx_freeze_contract_* database (set RGX_TEST_POSTGRES_DSN)
	go run ./cmd/testcontract-pg

test-pg-contract-keep: ## Same as test-pg-contract but keeps the disposable database for debugging
	go run ./cmd/testcontract-pg -keep

test-p0: ## Run P0 scenario regressions selected by TestP0_ naming
	go test -count=1 -shuffle=on -run '^TestP0_' ./...

test-contract: ## Run API, provider, repository, and streaming contract tests
	go test -count=1 -shuffle=on -run '^Test.*\(Contract\)$|^TestContract|^TestP0_' ./internal/handler/... ./internal/provider/... ./internal/repository/... ./internal/service/... ./internal/testquality/...

test-operational-contracts: ## Validate alert rules and PostgreSQL operational query plans
	go test -count=1 -run '^TestOBS_003_PrometheusAlertRulesMatchOperationalThresholds$' ./internal/obs
	go test -count=1 -run '^TestP0_PG_014_PostgresOperationalQueriesUsePartialIndexes$|^TestP0_PG_015_PostgresOperationalQueriesRemainIndexedAtTenTimesScale$' ./internal/repository

test-warning-contracts: ## Run opt-in capacity probes that report regressions without blocking merge
	RGX_QUERY_PLAN_100X_ENABLED=true go test -count=1 -run '^TestP1_PG_016_PostgresOperationalIndexSelectionAtHundredTimesScale$' ./internal/repository

test-race: ## Run the full backend suite with race detection
	CGO_ENABLED=1 go test -race -count=1 -shuffle=on ./...

test-coverage: ## Generate Go coverage profile
	@mkdir -p test-results
	set -o pipefail; \
	go test -json -count=1 -shuffle=on -coverpkg=./... -coverprofile=test-results/go-coverage.out -covermode=atomic ./... | tee test-results/go-test.json
	go tool cover -html=test-results/go-coverage.out -o test-results/go-coverage.html

test-frontend-coverage: ## Generate frontend coverage report
	cd web && npm run test:coverage

test-frontend-coverage-report: ## Generate full-source frontend coverage for changed-line attribution
	cd web && npm run test:coverage:report

test-e2e: ## Run browser E2E smoke tests
	cd web && npm run test:e2e

test-quality-report: ## Write machine-readable scenario governance report
	go run ./cmd/testquality-report -catalogue internal/testquality/testdata/scenario_catalogue.json -output test-results/scenario-quality-report.json

test-changed-coverage: test-frontend-coverage-report ## Evaluate Changed Lines Coverage against P0/P1 thresholds
	go run ./cmd/testquality-report -changed-coverage \
		-catalogue internal/testquality/testdata/scenario_catalogue.json \
		-output test-results/scenario-quality-report-changed.json \
		-base-ref origin/main \
		-go-coverage test-results/go-coverage.out \
		-frontend-coverage web/coverage-report/coverage-report-final.json

test-flake-report: ## Evaluate P0/P1 active flake evidence
	@if [ -z "$(FLAKE_INPUTS)" ]; then \
		echo "FLAKE_INPUTS is required for test-flake-report"; \
		exit 1; \
	fi
	go run ./cmd/testquality-flake \
		-catalogue internal/testquality/testdata/scenario_catalogue.json \
		-output test-results/flake-report.json \
		$(FLAKE_INPUTS)

test-coverage-trend: ## Generate package and frontend statement coverage trend
	go run ./cmd/testquality-coverage-trend \
		-repository . \
		-go-coverage test-results/go-coverage.out \
		-frontend-coverage web/coverage/coverage-final.json \
		-targets internal/testquality/testdata/coverage_targets.json \
		-output test-results/coverage-trend.json \
		-previous internal/testquality/testdata/coverage_trend_baseline.json \
		-history test-results/coverage-trend-history.json

test-query-plan-trend: ## Generate cross-run PostgreSQL query plan runtime trend
	go run ./cmd/testquality-query-plan-trend \
		-report-1x test-results/postgres-query-plan-report-1x.json \
		-report-10x test-results/postgres-query-plan-report-10x.json \
		-report-100x test-results/postgres-query-plan-report-100x.json \
		-output test-results/query-plan-trend.json \
		-history test-results/query-plan-trend-history.json \
		-commit "$(COMMIT)"

test-governance-report: ## Merge catalogue, coverage, flake, runtime, and mutation evidence
	go run ./cmd/testquality-governance \
		-catalogue internal/testquality/testdata/scenario_catalogue.json \
		-flake-report test-results/flake-report.json \
		-changed-coverage test-results/scenario-quality-report-changed.json \
		-coverage-trend test-results/coverage-trend.json \
		-query-plan-trend test-results/query-plan-trend.json \
		-mutation-evidence internal/testquality/testdata/mutation_sample.json \
		-output test-results/governance-report.json

check-nightly: ## Run nightly quality gate
	$(MAKE) vet build test test-integration test-race test-coverage test-frontend-coverage test-changed-coverage test-coverage-trend test-query-plan-trend test-flake-report test-governance-report test-e2e

check-release: ## Run release-candidate quality gate
	$(MAKE) lint check test-integration test-race test-coverage test-frontend-coverage test-e2e deploy-drill

deploy-drill: ## Run deployment readiness drill and write reports
	go run ./cmd/deploy-drill --config config/config.yaml --base-url http://localhost:9191 --output doc/deployment/drill

deploy-drill-production: ## Run deployment drill including production security baseline
	go run ./cmd/deploy-drill --config config/config.yaml --base-url http://localhost:9191 --output doc/deployment/drill --security-baseline

release-drill: ## Rehearse release packaging locally (make release-drill VERSION=v0.2.0-drill)
	bash $(SCRIPTS)/release-drill.sh $(VERSION)

# ─── Lint & Vet ──────────────────────────────────────────────────────
lint: ## Run golangci-lint
	golangci-lint run ./...

vet: ## Run go vet
	go vet ./...

# ─── Typecheck ───────────────────────────────────────────────────────
typecheck: ## Typecheck frontend
	cd web && npm run typecheck

# ─── Check (all-in-one) ─────────────────────────────────────────────
check: vet build test typecheck test-operational-contracts ## Run vet + build + test + typecheck + operational contracts

# ─── Dependency management ───────────────────────────────────────────
tidy: ## Run go mod tidy
	go mod tidy

# ─── Clean ───────────────────────────────────────────────────────────
clean: ## Remove build artifacts
	rm -rf bin/ dist/ web/dist/ run/ logs/

# ─── Docker / Compose ────────────────────────────────────────────────
docker-build: ## Build the server image with docker compose
	docker compose -f docker/docker-compose.yml build

docker-up: ## Start the stack (postgres + redis + ragflow-x)
	docker compose -f docker/docker-compose.yml up -d

docker-down: ## Stop the stack (keep volumes)
	docker compose -f docker/docker-compose.yml down

docker-log: ## Tail ragflow-x logs
	docker compose -f docker/docker-compose.yml logs -f ragflow-x

docker-ps: ## Show stack status
	docker compose -f docker/docker-compose.yml ps

docker-config: ## Validate the compose file
	docker compose -f docker/docker-compose.yml config

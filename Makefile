COMPOSE_FILE := docker-compose.integration.yml

.PHONY: integration-test integration-env integration-env-down integration-logs

integration-test:
	@docker compose -f $(COMPOSE_FILE) --profile test up \
		--build --abort-on-container-exit --exit-code-from test-runner; \
	rc=$$?; \
	docker compose -f $(COMPOSE_FILE) --profile test down -v; \
	exit $$rc

integration-env:
	NAVARIS_HOST_PORT=8080 docker compose -f $(COMPOSE_FILE) --profile dev up -d --build incus navarisd-dev
	@echo ""
	@echo "Navaris API: http://localhost:8080"
	@echo "Token:       test-token"
	@echo ""
	@echo "Run tests:"
	@echo "  NAVARIS_API_URL=http://localhost:8080 NAVARIS_TOKEN=test-token go test -tags integration ./test/integration/ -v"
	@echo ""
	@echo "Tear down:"
	@echo "  make integration-env-down"

integration-env-down:
	NAVARIS_HOST_PORT=8080 docker compose -f $(COMPOSE_FILE) --profile dev down -v

integration-logs:
	docker compose -f $(COMPOSE_FILE) logs -f

FC_COMPOSE_FILE := docker-compose.integration-firecracker.yml

.PHONY: integration-test-firecracker integration-env-firecracker integration-env-firecracker-down integration-logs-firecracker

integration-test-firecracker:
	@docker compose -f $(FC_COMPOSE_FILE) --profile test up \
		--build --abort-on-container-exit --exit-code-from test-runner; \
	rc=$$?; \
	docker compose -f $(FC_COMPOSE_FILE) --profile test down -v; \
	exit $$rc

integration-env-firecracker:
	NAVARIS_HOST_PORT=8080 docker compose -f $(FC_COMPOSE_FILE) --profile dev up -d --build navarisd-dev
	@echo ""
	@echo "Navaris API (Firecracker): http://localhost:8080"
	@echo "Token:                     test-token"
	@echo ""
	@echo "Run tests:"
	@echo "  NAVARIS_API_URL=http://localhost:8080 NAVARIS_TOKEN=test-token NAVARIS_SKIP_SNAPSHOTS=1 NAVARIS_SKIP_PORTS=1 go test -tags integration ./test/integration/ -v"
	@echo ""
	@echo "Tear down:"
	@echo "  make integration-env-firecracker-down"

integration-env-firecracker-down:
	NAVARIS_HOST_PORT=8080 docker compose -f $(FC_COMPOSE_FILE) --profile dev down -v

integration-logs-firecracker:
	docker compose -f $(FC_COMPOSE_FILE) logs -f

MIXED_COMPOSE_FILE := docker-compose.integration-mixed.yml

.PHONY: integration-test-mixed integration-env-mixed integration-env-mixed-down integration-logs-mixed

integration-test-mixed:
	@docker compose -f $(MIXED_COMPOSE_FILE) --profile test up \
		--build --abort-on-container-exit --exit-code-from test-runner; \
	rc=$$?; \
	docker compose -f $(MIXED_COMPOSE_FILE) --profile test down -v; \
	exit $$rc

integration-env-mixed-down:
	docker compose -f $(MIXED_COMPOSE_FILE) --profile test down -v

integration-logs-mixed:
	docker compose -f $(MIXED_COMPOSE_FILE) logs -f

# ---- All-in-one Docker image ----

.PHONY: docker-build docker-up docker-up-kvm docker-down

docker-build:
	docker build -f Dockerfile.navarisd-firecracker -t navarisd-firecracker .
	docker build -t navaris .

docker-up: docker-build
	docker compose --profile default up

docker-up-kvm: docker-build
	docker compose --profile kvm up

docker-down:
	docker compose --profile default --profile kvm down

# ─── Web UI ──────────────────────────────────────────────────────────────

.PHONY: web-deps web-dev web-build web-test web-clean build-ui

web-deps:
	cd web && npm install

web-dev:
	cd web && npm run dev

# web-build compiles the SPA and drops it into internal/webui/dist/ so the
# Go embed.FS directive in internal/webui/embed.go picks it up. We recreate
# the .gitkeep sentinel at the end because embed requires at least one file
# present at compile time and the sentinel keeps git status clean.
web-build:
	cd web && npm run build
	rm -rf internal/webui/dist
	mkdir -p internal/webui/dist
	cp -a web/dist/. internal/webui/dist/
	touch internal/webui/dist/.gitkeep

web-test:
	cd web && npm test -- --run

# web-clean nukes the build output and restores the empty dist/ with its
# sentinel so non-UI go builds still compile.
web-clean:
	rm -rf web/dist web/node_modules internal/webui/dist
	mkdir -p internal/webui/dist
	touch internal/webui/dist/.gitkeep

# build-ui produces a navarisd binary with the SPA embedded. The tag set
# matches the docker image: withui for embedded assets, firecracker and
# incus for both providers. CGO_ENABLED=0 keeps the binary statically linked.
build-ui: web-build
	CGO_ENABLED=0 go build -tags withui,firecracker,incus -o navarisd ./cmd/navarisd

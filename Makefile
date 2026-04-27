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

INCUS_COW_COMPOSE_FILE := docker-compose.integration-incus-cow.yml

.PHONY: integration-test-incus-cow integration-env-incus-cow integration-env-incus-cow-down integration-logs-incus-cow

integration-test-incus-cow:
	@docker compose -f $(INCUS_COW_COMPOSE_FILE) --profile test up \
		--build --abort-on-container-exit --exit-code-from test-runner; \
	rc=$$?; \
	docker compose -f $(INCUS_COW_COMPOSE_FILE) --profile test down -v; \
	exit $$rc

integration-env-incus-cow:
	NAVARIS_HOST_PORT=8080 docker compose -f $(INCUS_COW_COMPOSE_FILE) --profile dev up -d --build incus navarisd-dev
	@echo ""
	@echo "Navaris API (Incus + btrfs CoW): http://localhost:8080"
	@echo "Token:                            test-token"
	@echo ""
	@echo "Run tests:"
	@echo "  NAVARIS_API_URL=http://localhost:8080 NAVARIS_TOKEN=test-token go test -tags integration ./test/integration/ -v"
	@echo ""
	@echo "Tear down:"
	@echo "  make integration-env-incus-cow-down"

integration-env-incus-cow-down:
	NAVARIS_HOST_PORT=8080 docker compose -f $(INCUS_COW_COMPOSE_FILE) --profile dev down -v

integration-logs-incus-cow:
	docker compose -f $(INCUS_COW_COMPOSE_FILE) logs -f

FC_COW_COMPOSE_FILE := docker-compose.integration-firecracker-cow.yml

.PHONY: integration-test-firecracker-cow integration-env-firecracker-cow integration-env-firecracker-cow-down integration-logs-firecracker-cow

# Mounts a btrfs loop file at /srv/firecracker inside the navarisd container
# and runs navarisd with --storage-mode=reflink (strict). If FICLONE doesn't
# work on /srv/firecracker, navarisd refuses to start — so a healthy stack
# is itself proof that ReflinkBackend.CloneFile is exercised. After the
# integration suite passes we additionally run check-reflink-sharing.sh
# inside the navarisd container, which uses btrfs filesystem du --raw to
# directly assert that the snapshot/sandbox rootfs files share extents
# with their source images. That catches a kernel-level regression where
# FICLONE silently full-copies (which the strict probe wouldn't detect).
integration-test-firecracker-cow:
	@docker compose -f $(FC_COW_COMPOSE_FILE) --profile test up \
		--build --abort-on-container-exit --exit-code-from test-runner; \
	rc=$$?; \
	if [ $$rc -eq 0 ]; then \
		echo "==> Verifying btrfs reflink extent sharing..."; \
		docker compose -f $(FC_COW_COMPOSE_FILE) start navarisd >/dev/null 2>&1; \
		for i in 1 2 3 4 5 6 7 8 9 10; do \
			docker compose -f $(FC_COW_COMPOSE_FILE) ps navarisd | grep -q running && break; \
			sleep 1; \
		done; \
		docker compose -f $(FC_COW_COMPOSE_FILE) exec -T navarisd /usr/local/bin/check-reflink-sharing.sh \
			|| { echo "==> reflink-sharing assertion FAILED"; rc=2; }; \
	fi; \
	docker compose -f $(FC_COW_COMPOSE_FILE) --profile test down -v; \
	exit $$rc

integration-env-firecracker-cow:
	NAVARIS_HOST_PORT=8080 docker compose -f $(FC_COW_COMPOSE_FILE) --profile dev up -d --build navarisd-dev
	@echo ""
	@echo "Navaris API (Firecracker + btrfs reflink): http://localhost:8080"
	@echo "Token:                                      test-token"
	@echo ""
	@echo "Run tests:"
	@echo "  NAVARIS_API_URL=http://localhost:8080 NAVARIS_TOKEN=test-token NAVARIS_SKIP_SNAPSHOTS=1 NAVARIS_SKIP_PORTS=1 go test -tags integration ./test/integration/ -v"
	@echo ""
	@echo "Tear down:"
	@echo "  make integration-env-firecracker-cow-down"

integration-env-firecracker-cow-down:
	NAVARIS_HOST_PORT=8080 docker compose -f $(FC_COW_COMPOSE_FILE) --profile dev down -v

integration-logs-firecracker-cow:
	docker compose -f $(FC_COW_COMPOSE_FILE) logs -f

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

# e2e-local runs the local-only e2e boost tests against a navarisd you've
# already started outside docker-in-docker (e.g. via `./navarisd ...` on
# your dev machine). These tests cover paths that are blocked in CI's
# docker-in-docker environment: Incus memory limits at create-time, and
# Firecracker memory grow boosts that need --firecracker-mem-headroom-mult.
#
# Usage:
#   # Terminal 1: start a local navarisd with headroom for FC grow tests
#   ./navarisd \
#       --listen=:8080 --auth-token=test-token \
#       --firecracker-bin=... --jailer-bin=... --kernel-path=... \
#       --image-dir=... --chroot-base=/srv/firecracker \
#       --firecracker-mem-headroom-mult=2.0 \
#       --incus-socket=/var/lib/incus/unix.socket
#
#   # Terminal 2: drive the tests
#   make e2e-local NAVARIS_API_URL=http://localhost:8080 NAVARIS_BASE_IMAGE=alpine-3.21
.PHONY: e2e-local
e2e-local:
	@if [ -z "$$NAVARIS_API_URL" ] || [ -z "$$NAVARIS_BASE_IMAGE" ]; then \
		echo "set NAVARIS_API_URL and NAVARIS_BASE_IMAGE; see Makefile e2e-local target for an example"; \
		exit 1; \
	fi
	NAVARIS_E2E_LOCAL=1 NAVARIS_TOKEN=$${NAVARIS_TOKEN:-test-token} \
		go test -tags integration -v -run 'TestBoost_E2E_Local|TestBoost_E2E_Incus_CPU' ./test/integration/

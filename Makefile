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

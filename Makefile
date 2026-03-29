COMPOSE_FILE := docker-compose.integration.yml

.PHONY: integration-test integration-env integration-env-down integration-logs

integration-test:
	@docker compose -f $(COMPOSE_FILE) --profile test up \
		--build --abort-on-container-exit --exit-code-from test-runner; \
	rc=$$?; \
	docker compose -f $(COMPOSE_FILE) --profile test down -v; \
	exit $$rc

integration-env:
	NAVARIS_HOST_PORT=8080 docker compose -f $(COMPOSE_FILE) up -d --build incus navarisd
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
	NAVARIS_HOST_PORT=8080 docker compose -f $(COMPOSE_FILE) down -v

integration-logs:
	docker compose -f $(COMPOSE_FILE) logs -f

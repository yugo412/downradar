# .RECIPEPREFIX removes the dependency on literal tab characters, which are
# easy to lose when a file is copied, pasted or reformatted.
.RECIPEPREFIX = >

COMPOSE := docker compose -f compose.dev.yml

.DEFAULT_GOAL := help

# Every target below is a command, not a file, and one of them shares its name
# with a directory in the repository. Without this, make finds that directory,
# decides the target is up to date, and reports success without running
# anything.
.PHONY: help dev up reload rebuild down reset restart-api reset-limits logs \
        logs-api logs-web ps psql sql shell-api shell-web test test-local fmt \
        vet tidy deploy mail

help:
> @grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

dev: ## Run every service in the foreground (logs streamed)
> $(COMPOSE) up --build

up: ## Run every service in the background
> $(COMPOSE) up -d --build

reload: ## Recreate containers so changes to .env take effect
> $(COMPOSE) up -d

rebuild: ## After switching branches: recreate api and web so mounts, profiles and caches are fresh
> $(COMPOSE) up -d --force-recreate api web

down: ## Stop containers, keep all data
> $(COMPOSE) down

reset: ## Stop containers AND delete all data (database, caches, node_modules)
> $(COMPOSE) down -v

restart-api: ## Restart the api process (does NOT reload .env, use `make reload`)
> $(COMPOSE) restart api

reset-limits: ## Clear the in-memory rate limit buckets
> @echo "Rate limit buckets live in memory, so restarting the api clears them."
> $(COMPOSE) restart api >/dev/null
> @echo "Done. The next requests from your IP will be allowed again."

logs: ## Follow logs from every service
> $(COMPOSE) logs -f

logs-api: ## Follow backend logs
> $(COMPOSE) logs -f api

logs-web: ## Follow frontend logs
> $(COMPOSE) logs -f web

ps: ## Show container status
> $(COMPOSE) ps

psql: ## Open psql
> $(COMPOSE) exec db psql -U downradar -d downradar

sql: ## Run a one-off query: make sql Q="select * from services limit 5"
> $(COMPOSE) exec db psql -U downradar -d downradar -c "$(Q)"

shell-api: ## Shell inside the backend container
> $(COMPOSE) exec api sh

shell-web: ## Shell inside the frontend container
> $(COMPOSE) exec web sh

test: ## Run backend tests inside the container
> $(COMPOSE) exec api go test ./...

test-local: ## Run backend tests on the host
> cd backend && go test ./...

fmt: ## Format Go code
> cd backend && gofmt -w .

vet: ## Run go vet
> cd backend && go vet ./...

tidy: ## Tidy go.mod and go.sum
> cd backend && go mod tidy

deploy: ## Build the images here, push them, and roll the server to them
> ./deploy/deploy.sh

mail: ## Print the development inbox URL
> @echo "Mailpit: http://localhost:8026"

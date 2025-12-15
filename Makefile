
POSTGRES_PASSWORD ?= password

# https://github.com/golang-migrate/migrate/blob/master/database/postgres/TUTORIAL.md
.PHONY: migrate-up
migrate-up:
	@migrate -database postgres://jobber:$(POSTGRES_PASSWORD)@localhost:5432/jobber?sslmode=disable -path db/migrations up

.PHONY: migrate-down
migrate-down:
	@migrate -database postgres://jobber:$(POSTGRES_PASSWORD)@localhost:5432/jobber?sslmode=disable -path db/migrations down 1

.PHONY: check
check: lint test

.PHONY: test
test:
	@go test ./...

.PHONY: lint
lint:
	@golangci-lint run

.PHONY: init
init: build migrate-up

.PHONY: build
build:
	POSTGRES_PASSWORD=$(POSTGRES_PASSWORD) docker compose up -d

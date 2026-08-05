# Repository Guidelines

## Project Structure & Module Organization

This repository is a Go URL shortener service. `main.go` wires the Gin server, database, Redis, middleware, and background jobs. Application code lives under `src/`: `handler/` contains HTTP route handlers, `service/` holds short-link, hashing, Bloom filter, and visit-count logic, `database/` wraps MySQL/GORM setup, `config/` loads settings, and `middleware/` contains request middleware. Runtime configuration is in `config/app.yaml`. Docker assets are `Dockerfile` and `docker_compose_config.yml`.

## Build, Test, and Development Commands

- `go mod tidy`: sync `go.mod` and `go.sum` after dependency changes.
- `go run .`: run the service locally using `config/app.yaml`.
- `go build ./...`: compile all packages and catch type/import errors.
- `go test ./...`: run all Go tests.
- `docker compose -f docker_compose_config.yml up --build`: build and run the container stack defined for the project.

Local runs expect reachable MySQL and Redis instances matching `config/app.yaml`; change local credentials in that file only for development.

## Coding Style & Naming Conventions

Use standard Go formatting: run `gofmt` on changed `.go` files before committing. Package names should stay short, lowercase, and aligned with their folders (`service`, `handler`, `database`). Exported identifiers need concise Go doc comments when they form reusable package API. Prefer explicit error returns and early validation in handlers and services. Keep configuration keys lowercase YAML fields grouped by backend, as in `mysql.dsn` and `redis.addr`.

## Testing Guidelines

Tests should use Go's standard testing framework and live beside the code they cover as `*_test.go`. Name tests by behavior, for example `TestGenerateShortLinkRejectsInvalidURL`. Use table-driven tests for hash, expiration, and validation cases. For database or Redis behavior, isolate integration tests so `go test ./...` remains dependable for contributors without external services.

## Commit & Pull Request Guidelines

Recent history uses short, imperative commit subjects such as `Add expiration support for short URLs` and occasional scoped prefixes like `dev: Add Bloom Filter for short link validation`. Keep subjects under roughly 72 characters and describe the behavior changed.

Pull requests should include a brief summary, test results (`go test ./...`, Docker run notes if relevant), and any configuration or migration impact. Link related issues when available. Include screenshots or sample API requests/responses when changing HTTP behavior.

## Security & Configuration Tips

Do not commit production DSNs, Redis passwords, or private hostnames. Treat `config/app.yaml` as a development default and document production overrides in deployment tooling. Validate user-provided URLs in handlers/services before storage or redirects.

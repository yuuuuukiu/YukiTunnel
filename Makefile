# YukiTunnel developer shortcuts. Run `make help` for the list.
SHELL := /bin/bash
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0)

.PHONY: help build web desktop sidecars test test-go test-smoke test-e2e test-e2e-local test-live vet fmt docker docker-dev clean dev-server dev-web keygen

help:
	@echo "YukiTunnel targets:"
	@echo "  make build        Build WebUI + yuki + yuki-server into bin/"
	@echo "  make web          Build the WebUI only"
	@echo "  make desktop      Build the desktop frontend (pnpm)"
	@echo "  make sidecars     Copy Go binaries for Tauri and build bundles"
	@echo "  make test         go test ./..."
	@echo "  make test-smoke   End-to-end CLI + traffic smoke test"
	@echo "  make test-e2e     Full stack E2E (docker compose + CLI + tunnel)"
	@echo "  make test-e2e-local  Same, without Docker"
	@echo "  make test-live    Real cloud live test (needs credentials)"
	@echo "  make vet fmt      Static checks / formatting"
	@echo "  make docker       docker compose build"
	@echo "  make dev-server   Run a local server on :18443 with demo data"
	@echo "  make dev-web      Run the WebUI dev server (proxies to :18443)"
	@echo "  make keygen       Generate the profile signing key pair"
	@echo "  make clean        Remove build outputs"

build:
	bash scripts/build.sh

web:
	cd web && pnpm install --frozen-lockfile && pnpm build

desktop:
	cd desktop && pnpm install --frozen-lockfile && pnpm build

sidecars:
	bash scripts/prepare-sidecars.sh

test:
	go test ./... -timeout 180s

test-go:
	go test ./internal/... -race -timeout 180s

test-smoke:
	bash scripts/smoke.sh

test-e2e:
	bash scripts/e2e-local.sh

test-e2e-local:
	bash scripts/e2e-local.sh --no-docker

test-live:
	@test -n "$$YUKI_ALIYUN_ACCESS_KEY_ID" || (echo "set YUKI_ALIYUN_ACCESS_KEY_ID / YUKI_ALIYUN_ACCESS_KEY_SECRET first" >&2; exit 2)
	bash scripts/live-test-aliyun.sh

vet:
	go vet ./...
	cd web && pnpm exec tsc -b --noEmit

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './desktop/*')
	cd web && pnpm exec oxlint --fix src || true

docker:
	docker compose build

docker-dev:
	docker compose -f docker-compose.yml -f docker-compose.dev.yml up --build

dev-server:
	@mkdir -p /tmp/yukitunnel-dev
	@test -f /tmp/yukitunnel-dev/config.yaml || \
	  go run ./cmd/yuki-server init --data-dir /tmp/yukitunnel-dev \
	    --api-addr 127.0.0.1:18443 --control-addr 127.0.0.1:19443 \
	    --admin-password admin12345 --port-min 28000 --port-max 28100
	go run ./cmd/yuki-server serve --data-dir /tmp/yukitunnel-dev

dev-web:
	cd web && YUKI_DEV_SERVER=https://127.0.0.1:18443 pnpm dev

keygen:
	bash scripts/gen-profile-signing-key.sh

clean:
	rm -rf bin web/dist desktop/dist desktop/src-tauri/binaries
	@echo "cleaned"

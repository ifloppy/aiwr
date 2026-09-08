BIN ?= aiwr
GO ?= go

.PHONY: all build test vet fmt lint lint-fix format smoke smoke-synthid smoke-ctrlregen smoke-markllm smoke-markdiffusion install upstream-check package-check \
	package-deb package-rpm package-apk package-arch package-all package-release \
	serve compose-up compose-check docker-core-build docker-core-help \
	bench-synthid-text bench-full bench-semantic install-skill \
	install-claude-code-skill install-claude-code-text-skill install-claude-project-skill \
	package-cowork-skill package-cowork-text-skill install-cursor-text-skill plugin-validate clean

all: test build

build:
	$(GO) build -trimpath -o $(BIN) ./cmd/aiwr

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

lint: vet

lint-fix:
	$(GO) fmt ./...

format:
	test -z "$$(gofmt -l cmd internal)"

fmt:
	test -z "$$(gofmt -l cmd internal)"

smoke: build
	@tmp="$$(mktemp -d)"; \
	printf '%b' 'hello\u200bworld\n' > "$$tmp/note.txt"; \
	./$(BIN) clean-text "$$tmp/note.txt" --json >/dev/null; \
	test -f "$$tmp/note.cleaned.txt"; \
	printf '%s\n' 'smoke ok'

# The upstream Makefile names these optional smoke checks, but the referenced
# shell scripts are not present in the current upstream tree. Keep the target
# surface usable by validating the corresponding adapter command/help path;
# real model execution remains an explicit, user-configured operation.
smoke-synthid: build
	./$(BIN) score-synthid --help >/dev/null

smoke-ctrlregen: build
	./$(BIN) clean-ctrlregen --help >/dev/null

smoke-markllm: build
	./$(BIN) detect-text-watermark --help >/dev/null

smoke-markdiffusion: build
	./$(BIN) markdiffusion --help >/dev/null

install:
	$(GO) install ./cmd/aiwr

serve: build
	./$(BIN) serve

upstream-check:
	./scripts/check-upstream.sh

package-check:
	./scripts/check-packaging.sh

package-deb:
	GO="$(GO)" bash scripts/package-nfpm.sh deb

package-rpm:
	GO="$(GO)" bash scripts/package-nfpm.sh rpm

package-apk:
	GO="$(GO)" bash scripts/package-nfpm.sh apk

package-arch:
	GO="$(GO)" bash scripts/package-nfpm.sh archlinux

package-all:
	GO="$(GO)" bash scripts/package-nfpm.sh all

package-release:
	goreleaser release --snapshot --clean

docker-core-build:
	docker build -f service/Dockerfile -t aiwr .

docker-core-help:
	docker run --rm aiwr --help

compose-up:
	docker compose up --build -d

compose-check:
	./compose-check.sh

bench-synthid-text: build
	@if test -z "$(MARKLLM_DIR)"; then \
		echo "bench-synthid-text skipped (set MARKLLM_DIR; see docs/synthid-text-benchmark.md)"; \
	else \
		./$(BIN) bench-synthid-text --upstream-scripts service/scripts --markllm-dir "$(MARKLLM_DIR)"; \
	fi

bench-full: build
	@if test -z "$(MARKLLM_DIR)"; then \
		echo "bench-full skipped (set MARKLLM_DIR; see docs/synthid-text-benchmark.md)"; \
	else \
		./$(BIN) bench-synthid-text --upstream-scripts service/scripts --markllm-dir "$(MARKLLM_DIR)" --corpus benchmarks/corpus-large --docs 20 --seeds 3 --max-new-tokens 300 --target-margin 0.03 --require-semantic --mode strategy --coverage-floor 0.5 --eval-split 0.8 --humanize-intensity 0.4 --rewrite-backend "$(REWRITE_BACKEND)" --rewrite-model "$(REWRITE_MODEL)" --rewrite-base-url "$(REWRITE_BASE_URL)" $(REWRITE_ALLOW_REMOTE); \
	fi

bench-semantic: build
	@if test -z "$(MARKLLM_DIR)"; then \
		echo "bench-semantic skipped (set MARKLLM_DIR)"; \
	else \
		./$(BIN) bench-synthid-text --upstream-scripts service/scripts --markllm-dir "$(MARKLLM_DIR)" --require-semantic; \
	fi

# Agent skill and plugin compatibility targets are kept at the repository
# level, just like upstream. The Go binary remains the default file service.
install-skill:
	mkdir -p $(HOME)/.grok/skills
	ln -sfn $(CURDIR)/skills/remove-ai-marks $(HOME)/.grok/skills/remove-ai-marks
	@echo "linked -> $(HOME)/.grok/skills/remove-ai-marks"

install-claude-code-skill:
	python3 install_skill.py --skill remove-ai-marks --target claude-code

install-claude-code-text-skill:
	python3 install_skill.py --skill clean-user-facing-text --target claude-code

install-claude-project-skill:
	python3 install_skill.py --skill remove-ai-marks --target claude-project --project-dir $(or $(PROJECT),$(CURDIR))

package-cowork-skill:
	python3 install_skill.py --skill remove-ai-marks --target cowork --force

package-cowork-text-skill:
	python3 install_skill.py --skill clean-user-facing-text --target cowork --force

install-cursor-text-skill:
	python3 install_skill.py --skill clean-user-facing-text --target cursor

plugin-validate:
	claude plugin validate . --strict

clean:
	rm -rf dist
	find . -type d -name __pycache__ -prune -exec rm -rf {} + 2>/dev/null || true
	rm -rf .pytest_cache .venv

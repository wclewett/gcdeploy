export GO111MODULE=on
# update app name. this is the name of binary
APP=gcdeplou
APP_EXECUTABLE="./out/$(APP)"
ALL_PACKAGES=$(shell go list ./... | grep -v /vendor)
SHELL := /bin/bash # Use bash syntax

# Optional if you need DB and migration commands
# DB_HOST=$(shell cat config/application.yml | grep -m 1 -i HOST | cut -d ":" -f2)
# DB_NAME=$(shell cat config/application.yml | grep -w -i NAME  | cut -d ":" -f2)
# DB_USER=$(shell cat config/application.yml | grep -i USERNAME | cut -d ":" -f2)

# Optional colors to beautify output
GREEN  := $(shell tput -Txterm setaf 2)
YELLOW := $(shell tput -Txterm setaf 3)
WHITE  := $(shell tput -Txterm setaf 7)
CYAN   := $(shell tput -Txterm setaf 6)
RESET  := $(shell tput -Txterm sgr0)

## Quality
check-quality: ## runs code quality checks
	make tidy
	make lint
	make fmt
	make vet

# Append || true below if blocking local developement
lint: ## go linting. Update and use specific lint tool and options
	golangci-lint run 

vet: ## go vet
	go vet ./...

fmt: ## runs go formatter
	go fmt ./...

tidy: ## runs tidy to fix go.mod dependencies
	go mod tidy

## Test
test: ## runs tests and create generates coverage report
	go test ./... -coverprofile=coverage.out
	
coverage: ## displays test coverage report in html mode
	make test
	go tool cover -func=coverage.out

## Build
build-local: ## build the go application
	make fmt
	mkdir -p out/
	go build -o $(APP_EXECUTABLE)
	@echo "Build passed"

install:
	go install .

clean: ## cleans binary and other generated files
	go clean
	rm -rf out/
	rm -f coverage*.out
	rm -f local.db

vendor: ## all packages required to support builds and tests in the /vendor directory
	go mod vendor

# [Optional] mock generation via go generate
# generate_mocks:
# 	go generate -x `go list ./... | grep - v wire`

# [Optional] Database commands
## Database
migrate: build
	${APP_EXECUTABLE} migrate --config=config/application.test.yml

rollback: build
	${APP_EXECUTABLE} migrate --config=config/application.test.yml

.PHONY: all test build vendor
## All
all: ## runs setup, quality checks and builds
	make check-quality
	make test
	make build

lines:
	find . -name "*.go" | xargs wc -l
	find . -name "*_test.go" | xargs wc -l
	find . -name "*.templ" | xargs wc -l
	find . -name "*input.css" | xargs wc -l

.PHONY: help
## Help
help: ## Show this help.
	@echo ''
	@echo 'Usage:'
	@echo '  ${YELLOW}make${RESET} ${GREEN}<target>${RESET}'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} { \
		if (/^[a-zA-Z_-]+:.*?##.*$$/) {printf "    ${YELLOW}%-20s${GREEN}%s${RESET}\n", $$1, $$2} \
		else if (/^## .*$$/) {printf "  ${CYAN}%s${RESET}\n", substr($$1,4)} \
		}' $(MAKEFILE_LIST)

.PHONY: run lint lint-check-deps

run: 
	@echo "[demoware] running with default params"
	@go run main.go \
		-with-auth-token=deadbeef \
		-with-random-error-prob=0.1

build:
	go build -o demoware

lint: lint-check-deps
	@echo "[golangci-lint] linting sources"
	@golangci-lint run \
		-E misspell \
		-E golint \
		-E gofmt \
		-E unconvert \
		--exclude-use-default=false \
		./...

lint-check-deps:
	@if [ -z `which golangci-lint` ]; then \
		echo "[go get] installing golangci-lint";\
		go get -u github.com/golangci/golangci-lint/cmd/golangci-lint;\
	fi

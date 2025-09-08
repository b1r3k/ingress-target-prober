VERSION := 0.1.0
APP=ingress-target-prober
PKG=ghcr.io/b1r3k/ingress-target-prober

.PHONY: test fmt vet tidy clean build

build:
	go build -ldflags="-X main.version=$$(git describe --tags --always 2>/dev/null || echo dev)" -o bin/$(APP) ./main.go

run:
	go run ./cmd/$(APP)

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -rf bin

build-image:
	docker buildx build --platform linux/arm64,linux/amd64 -t ghcr.io/b1r3k/ingress-target-prober:$(VERSION) --push .

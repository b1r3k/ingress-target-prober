VERSION := 0.1.2
APP=ingress-target-prober
PKG=ghcr.io/b1r3k/ingress-target-prober
GHCR_REPO_URI=ghcr.io
GHCR_REPO_USER=b1r3k

.PHONY: test fmt vet tidy clean build

ghcr-login:
	keyring get $(APP) ghcr_registry | docker login $(GHCR_REPO_URI) --username $(GHCR_REPO_USER) --password-stdin

build: tidy fmt vet test
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

build-image: ghcr-login
	docker buildx build --platform linux/arm64,linux/amd64 -t ghcr.io/b1r3k/ingress-target-prober:$(VERSION) -t ghcr.io/b1r3k/ingress-target-prober:latest --push .

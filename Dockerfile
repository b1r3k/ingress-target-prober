# syntax=docker/dockerfile:1
FROM golang:1.22 AS build
ARG TARGETPLATFORM

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN case "$TARGETPLATFORM" in \
        "linux/amd64")  GOARCH=amd64 ;; \
        "linux/arm64")  GOARCH=arm64 ;; \
        *) echo "Unsupported architecture: $TARGETPLATFORM" && exit 1 ;; \
    esac && \
    CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build -o /out/ingress-target-prober ./main.go

FROM gcr.io/distroless/static:nonroot
USER 65532:65532
COPY --from=build /out/ingress-target-prober /ingress-target-prober
ENTRYPOINT ["/ingress-target-prober"]

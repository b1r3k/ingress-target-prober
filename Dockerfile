# syntax=docker/dockerfile:1
FROM golang:1.22 AS build
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -o /out/ingress-target-prober ./main.go

FROM gcr.io/distroless/static:nonroot

LABEL org.opencontainers.image.source="https://github.com/b1r3k/ingress-target-prober"
LABEL org.opencontainers.image.description="Ingress target prober"

USER 65532:65532
COPY --from=build /out/ingress-target-prober /ingress-target-prober
ENTRYPOINT ["/ingress-target-prober"]

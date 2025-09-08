# syntax=docker/dockerfile:1
FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/ingress-target-prober ./main.go

FROM gcr.io/distroless/static:nonroot
USER 65532:65532
COPY --from=build /out/ingress-target-prober /ingress-target-prober
ENTRYPOINT ["/ingress-target-prober"]

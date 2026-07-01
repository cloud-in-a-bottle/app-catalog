FROM golang:1.24-alpine AS builder

WORKDIR /src

COPY go.mod .
COPY go.sum .
# Retry module download and fall back to direct VCS fetches: the default
# proxy.golang.org intermittently returns 403 from some hosting providers,
# which would otherwise fail the build non-deterministically.
RUN for i in 1 2 3; do \
        go mod download && break; \
        echo "go mod download failed (attempt $i), retrying with GOPROXY=direct"; \
        GOPROXY=direct go mod download && break; \
        sleep 5; \
    done

COPY . .
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/openhost-catalog ./cmd/openhost-catalog

FROM alpine:3.21
WORKDIR /app

COPY --from=builder /out/openhost-catalog /usr/local/bin/openhost-catalog
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/openhost-catalog"]

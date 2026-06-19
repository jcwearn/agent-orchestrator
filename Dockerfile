# Stage 1: Build web frontend
FROM node:24-slim@sha256:c2d5ade763cacfb03fe9cb8e8af5d1be5041ff331921fa26a9b231ca3a4f780a AS web-build
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: Build Go binary
FROM golang:1.26@sha256:792443b89f65105abba56b9bd5e97f680a80074ac62fc844a584212f8c8102c3 AS go-build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/embed.go web/embed.go
COPY --from=web-build /app/web/dist/ web/dist/
RUN CGO_ENABLED=0 go build -o /agent-orchestrator ./cmd/

# Stage 3: Runtime
FROM debian:bookworm-slim@sha256:96e378d7e6531ac9a15ad505478fcc2e69f371b10f5cdf87857c4b8188404716
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates curl && \
    rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://coder.com/install.sh | sh
COPY --from=go-build /agent-orchestrator /usr/local/bin/agent-orchestrator
RUN useradd -r -s /usr/sbin/nologin appuser
USER appuser
EXPOSE 8080
ENTRYPOINT ["agent-orchestrator"]

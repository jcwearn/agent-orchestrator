# Stage 1: Build web frontend
FROM node:22-slim AS web-build
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: Build Go binary
FROM golang:1.26@sha256:e2ddb153f786ee6210bf8c40f7f35490b3ff7d38be70d1a0d358ba64225f6428 AS go-build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/embed.go web/embed.go
COPY --from=web-build /app/web/dist/ web/dist/
RUN CGO_ENABLED=0 go build -o /agent-orchestrator ./cmd/

# Stage 3: Runtime
FROM debian:bookworm-slim
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates curl && \
    rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://coder.com/install.sh | sh
COPY --from=go-build /agent-orchestrator /usr/local/bin/agent-orchestrator
RUN useradd -r -s /usr/sbin/nologin appuser
USER appuser
EXPOSE 8080
ENTRYPOINT ["agent-orchestrator"]

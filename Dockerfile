# Stage 1: Build web frontend
FROM node:24-slim@sha256:e8e2e91b1378f83c5b2dd15f0247f34110e2fe895f6ca7719dbb780f929368eb AS web-build
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: Build Go binary
FROM golang:1.26@sha256:c7e98cc0fd4dfb71ee7465fee6c9a5f079163307e4bf141b336bb9dae00159a5 AS go-build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/embed.go web/embed.go
COPY --from=web-build /app/web/dist/ web/dist/
RUN CGO_ENABLED=0 go build -o /agent-orchestrator ./cmd/

# Stage 3: Runtime
FROM debian:bookworm-slim@sha256:74d56e3931e0d5a1dd51f8c8a2466d21de84a271cd3b5a733b803aa91abf4421
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates curl && \
    rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://coder.com/install.sh | sh
COPY --from=go-build /agent-orchestrator /usr/local/bin/agent-orchestrator
RUN useradd -r -s /usr/sbin/nologin appuser
USER appuser
EXPOSE 8080
ENTRYPOINT ["agent-orchestrator"]

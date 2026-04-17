# Stage 1: Build web frontend
FROM node:24-slim@sha256:879b21aec4a1ad820c27ccd565e7c7ed955f24b92e6694556154f251e4bdb240 AS web-build
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: Build Go binary
FROM golang:1.26@sha256:5f3787b7f902c07c7ec4f3aa91a301a3eda8133aa32661a3b3a3a86ab3a68a36 AS go-build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/embed.go web/embed.go
COPY --from=web-build /app/web/dist/ web/dist/
RUN CGO_ENABLED=0 go build -o /agent-orchestrator ./cmd/

# Stage 3: Runtime
FROM debian:bookworm-slim@sha256:4724b8cc51e33e398f0e2e15e18d5ec2851ff0c2280647e1310bc1642182655d
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates curl && \
    rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://coder.com/install.sh | sh
COPY --from=go-build /agent-orchestrator /usr/local/bin/agent-orchestrator
RUN useradd -r -s /usr/sbin/nologin appuser
USER appuser
EXPOSE 8080
ENTRYPOINT ["agent-orchestrator"]

# Stage 1: Build web frontend
FROM node:24-slim@sha256:06e5c9f86bfa0aaa7163cf37a5eaa8805f16b9acb48e3f85645b09d459fc2a9f AS web-build
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: Build Go binary
FROM golang:1.26@sha256:595c7847cff97c9a9e76f015083c481d26078f961c9c8dca3923132f51fe12f1 AS go-build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/embed.go web/embed.go
COPY --from=web-build /app/web/dist/ web/dist/
RUN CGO_ENABLED=0 go build -o /agent-orchestrator ./cmd/

# Stage 3: Runtime
FROM debian:bookworm-slim@sha256:f06537653ac770703bc45b4b113475bd402f451e85223f0f2837acbf89ab020a
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates curl && \
    rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://coder.com/install.sh | sh
COPY --from=go-build /agent-orchestrator /usr/local/bin/agent-orchestrator
RUN useradd -r -s /usr/sbin/nologin appuser
USER appuser
EXPOSE 8080
ENTRYPOINT ["agent-orchestrator"]

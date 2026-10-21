# syntax=docker/dockerfile:1.7
# ---------- WebUI ----------
FROM node:22-alpine AS web
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml* ./
RUN pnpm install --frozen-lockfile || pnpm install
COPY web/ ./
RUN pnpm build

# ---------- Go binaries ----------
FROM golang:1.24-alpine AS build
RUN apk add --no-cache git ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./internal/server/webdist
ARG VERSION=0.1.0
ARG COMMIT=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/yuuuuukiu/yukitunnel/internal/version.Version=${VERSION} -X github.com/yuuuuukiu/yukitunnel/internal/version.Commit=${COMMIT}" \
      -o /out/yuki-server ./cmd/yuki-server && \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/yuuuuukiu/yukitunnel/internal/version.Version=${VERSION}" \
      -o /out/yuki ./cmd/yuki

# ---------- Runtime ----------
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata wget && \
    addgroup -S yuki && adduser -S -G yuki -h /data yuki
COPY --from=build /out/yuki-server /usr/local/bin/yuki-server
COPY --from=build /out/yuki /usr/local/bin/yuki
COPY deploy/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh && mkdir -p /data && chown -R yuki:yuki /data
USER yuki
WORKDIR /data
ENV YUKI_DATA_DIR=/data \
    YUKI_API_ADDR=0.0.0.0:8443 \
    YUKI_CONTROL_ADDR=0.0.0.0:9443
VOLUME ["/data"]
EXPOSE 8443 9443
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD wget -qO- --no-check-certificate https://127.0.0.1:8443/api/v1/health || exit 1
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["serve"]

# syntax=docker/dockerfile:1

# ---- stage 1: build the web console (embedded by the hub via go:embed) ----
FROM node:20-bookworm AS web
WORKDIR /src/sys0-console
COPY sys0-console/package.json sys0-console/package-lock.json* ./
RUN npm ci || npm install
COPY sys0-console/ ./
# vite outDir = ../sys0-hub/web ; emit into a path we copy into the go stage
RUN npm run build && ls -la ../sys0-hub/web

# ---- stage 2: build the single hub binary (console embedded) ----
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# bring in the freshly built console so //go:embed all:web picks it up
COPY --from=web /src/sys0-hub/web ./sys0-hub/web
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/sys0-hub ./sys0-hub

# ---- stage 3: minimal runtime ----
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /data
COPY --from=build /out/sys0-hub /usr/local/bin/sys0-hub
# 8080 = console + REST/WS + MCP ; 7000 = agent TCP
EXPOSE 8080 7000
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/sys0-hub"]
CMD ["-http", ":8080", "-agent-tcp", ":7000", "-db", "/data/sys0.db"]

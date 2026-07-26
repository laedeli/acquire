# Build acquire (Go + embedded console).
#
# The console is a Vite/React app baked into the binary with go:embed, so the
# image stays a single static server — no nginx sidecar, no runtime asset fetch.
# A built copy is also committed under internal/httpapi/web so `go build` works
# without node; this stage rebuilds it so the image never ships a stale one.
FROM node:20-alpine AS web
WORKDIR /web
# The design system is a committed tarball, so npm needs no registry auth.
# Copy it with the manifests to keep the install layer cacheable.
COPY web/package.json web/package-lock.json* ./
COPY web/vendor ./vendor
RUN if [ -f package-lock.json ]; then npm ci --no-audit --no-fund; else npm install --no-audit --no-fund; fi
COPY web/ ./
RUN npm run build

FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
# vite writes to ../internal/httpapi/web, i.e. /internal/httpapi/web in the web
# stage. Overwrite the committed copy before go:embed reads it.
COPY --from=web /internal/httpapi/web ./internal/httpapi/web
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /acquire ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /acquire /acquire
EXPOSE 8080
ENTRYPOINT ["/acquire"]

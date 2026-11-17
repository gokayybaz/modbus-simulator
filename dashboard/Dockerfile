# syntax=docker/dockerfile:1
# 1) React/Vite UI
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# 2) Go API server
FROM golang:1.26-alpine AS api
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/dashboard ./cmd/dashboard

# 3) Runtime
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=api /out/dashboard /dashboard
COPY --from=web /web/dist /web
ENV STATIC_DIR=/web LISTEN_ADDR=:8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/dashboard"]

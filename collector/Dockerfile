# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/collector ./cmd/collector

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/collector /collector
USER nonroot:nonroot
ENTRYPOINT ["/collector"]

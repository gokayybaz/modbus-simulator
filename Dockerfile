# syntax=docker/dockerfile:1
# Modbus TCP simulator (root Go module).
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY configs ./configs
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/simulator ./cmd/simulator

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/simulator /simulator
COPY configs /configs
EXPOSE 1502
USER nonroot:nonroot
ENTRYPOINT ["/simulator"]
# Inside a container the server must listen on all interfaces.
CMD ["-addr", "0.0.0.0:1502"]

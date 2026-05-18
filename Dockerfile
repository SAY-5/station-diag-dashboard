# Multi-stage build. The build stage is fully static thanks to the pure-Go
# SQLite driver, so the final image needs no C runtime.
FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w" \
    -o /out/dashboard ./cmd/dashboard
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w" \
    -o /out/station-emitter ./cmd/station-emitter

FROM alpine:3.20

RUN adduser -D -u 10001 station
WORKDIR /app

COPY --from=build /out/dashboard /app/dashboard
COPY --from=build /out/station-emitter /app/station-emitter
COPY rules /app/rules

USER station
EXPOSE 8080 7000

ENTRYPOINT ["/app/dashboard"]

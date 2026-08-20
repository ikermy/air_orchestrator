# syntax=docker/dockerfile:1.7

FROM golang:1.25 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

ARG GO_BUILD_TAGS=""
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -tags "${GO_BUILD_TAGS}" \
    -ldflags="-s -w" -o /app/airorc ./cmd/

# UPX дополнительно сжимает статический Go-бинарник.
RUN apt-get update \
    && apt-get install -y --no-install-recommends upx-ucl \
    && upx --best --lzma /app/airorc \
    && rm -rf /var/lib/apt/lists/*

FROM alpine:3.22 AS runtime

RUN apk add --no-cache ca-certificates tzdata \
    && mkdir -p /app

WORKDIR /app

COPY --from=builder /app/airorc /app/airorc

ENV TZ=Europe/Amsterdam

EXPOSE 8080

ENTRYPOINT ["/app/airorc"]

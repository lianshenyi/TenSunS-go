# syntax=docker/dockerfile:1
FROM golang:1.24-alpine AS build

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags "-s -w -X github.com/lianshenyi/TenSunS-go/internal/version.Version=${VERSION} -X github.com/lianshenyi/TenSunS-go/internal/version.Commit=${COMMIT} -X github.com/lianshenyi/TenSunS-go/internal/version.Date=${BUILD_DATE}" \
    -o /out/tensuns ./cmd/tensuns

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S tensuns \
    && adduser -S -G tensuns tensuns
COPY --from=build /out/tensuns /usr/local/bin/tensuns
ENV TZ=Asia/Shanghai \
    port=2026
EXPOSE 2026
USER tensuns
ENTRYPOINT ["/usr/local/bin/tensuns"]

# syntax=docker/dockerfile:1

# ---- build stage: compile both entrypoints with the Go 1.25 toolchain ----
FROM golang:1.25-alpine AS build
WORKDIR /src

# Resolve modules first so this layer is cached when only sources change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ENV CGO_ENABLED=0
RUN go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/verify ./cmd/verify

# ---- runtime stage: one image, command selected by each Compose service ----
FROM alpine:3.22 AS runtime
RUN adduser -D -u 10001 appuser
WORKDIR /app
COPY --from=build /out/api /usr/local/bin/api
COPY --from=build /out/verify /usr/local/bin/verify
USER appuser

# The default command runs the long-lived API; the "verify" Compose service
# overrides it with the one-shot client.
EXPOSE 8080
ENTRYPOINT []
CMD ["api"]

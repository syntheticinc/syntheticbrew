FROM golang:1.25-alpine AS builder
ARG VERSION=dev-ce
ARG CACHE_BUST=1
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN echo "build: version=${VERSION} cache-bust=${CACHE_BUST}" && \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o syntheticbrew-ce ./cmd/ce

FROM node:22-alpine AS admin-builder
WORKDIR /admin
COPY admin/package.json admin/package-lock.json ./
RUN npm ci
COPY admin/ .
RUN npm run build

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /app/syntheticbrew-ce /usr/local/bin/
COPY --from=admin-builder /admin/dist /usr/share/syntheticbrew/admin

# Embeddable widget bundle is optional — copied in by CI/CD when present.
# Place built widget asset under widget/dist/widget.js to include it locally.

EXPOSE 8443 8444
ENTRYPOINT ["syntheticbrew-ce"]

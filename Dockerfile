# Build the web application first so it can be embedded in the Go binary.
FROM node:20-alpine AS web-build

WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS go-build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-build /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -o /out/eventpass ./cmd/eventpass

FROM alpine:3.22

RUN apk add --no-cache ca-certificates \
    && adduser -D -H -u 10001 eventpass \
    && mkdir -p /data \
    && chown eventpass:eventpass /data

WORKDIR /app
COPY --from=go-build /out/eventpass /app/eventpass

USER eventpass
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/app/eventpass"]
CMD ["serve"]

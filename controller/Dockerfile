FROM golang:1.23-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/lab-controller .

FROM alpine:3.21
RUN addgroup -S controller && adduser -S -G controller controller \
    && apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /out/lab-controller /app/lab-controller
# Docker Desktop commonly exposes the socket as root:root. The local demo controller
# needs socket access, so it intentionally runs as root inside this isolated container.
EXPOSE 8082
ENTRYPOINT ["/app/lab-controller"]


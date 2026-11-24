FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/payment-api .

FROM alpine:3.21
RUN addgroup -S payment && adduser -S -G payment payment
WORKDIR /app
COPY --from=build /out/payment-api /app/payment-api
USER payment
EXPOSE 8080
ENTRYPOINT ["/app/payment-api"]


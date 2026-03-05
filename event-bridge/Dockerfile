FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o event-bridge .

FROM alpine:3.19
RUN apk add --no-cache librdkafka-dev ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/event-bridge .
EXPOSE 8080
ENTRYPOINT ["./event-bridge"]

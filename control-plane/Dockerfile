FROM golang:1.24-alpine3.22 AS builder

RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /control-plane ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates
COPY --from=builder /control-plane /control-plane

EXPOSE 8085
ENTRYPOINT ["/control-plane"]
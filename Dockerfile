FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o helpdesk-agent .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/helpdesk-agent .
COPY --from=builder /app/config/llm.yaml ./config/
COPY .env .env
EXPOSE 8080
CMD ["./helpdesk-agent"]

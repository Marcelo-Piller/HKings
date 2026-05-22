# Build stage
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-w -s" -o server .

# Run stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates sqlite-libs

WORKDIR /app
COPY --from=builder /app/server .
COPY --from=builder /app/templates ./templates
COPY --from=builder /app/static ./static

EXPOSE 8080
ENV PORT=8080

CMD ["./server"]

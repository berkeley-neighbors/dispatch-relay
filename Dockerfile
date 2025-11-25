FROM golang AS builder

WORKDIR /app

COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -a -o main .

FROM alpine:latest

WORKDIR /root

# Copy the binary from builder
COPY --from=builder /app/main /root

# Create non-root user
RUN adduser -D -u 1000 runner && \
    chown -R runner:runner /root

USER runner

EXPOSE 4514
ENV GIN_MODE=release
ENV PORT=4514

CMD ["./main"]
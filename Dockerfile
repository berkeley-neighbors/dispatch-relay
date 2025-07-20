FROM golang

WORKDIR /app

EXPOSE 4514
ENV GIN_MODE=release
ENV PORT=4514

COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY . .

CMD ["go", "run", "main.go"]
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY *.html ./
RUN CGO_ENABLED=0 go build -o modelmux .

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/modelmux .
COPY --from=builder /app/*.html ./
EXPOSE 8000
CMD ["./modelmux"]

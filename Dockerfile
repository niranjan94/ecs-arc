FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /ecs-arc ./cmd/ecs-arc

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /ecs-arc /usr/local/bin/ecs-arc
ENTRYPOINT ["/usr/local/bin/ecs-arc"]
CMD ["controller"]

# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /workspace

# Copy dependency manifests
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY cmd/ cmd/
COPY pkg/ pkg/

# Build statically linked binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o tollgate cmd/tollgate/main.go

# Runtime stage using distroless nonroot
FROM gcr.io/distroless/static:nonroot

WORKDIR /
COPY --from=builder /workspace/tollgate .
USER 65532:65532

EXPOSE 9443 8080

ENTRYPOINT ["/tollgate"]

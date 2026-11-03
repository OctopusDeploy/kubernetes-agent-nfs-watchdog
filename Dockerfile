# syntax=docker/dockerfile:1
FROM golang:1.27 as build-debug-base

# Pinned rather than tracking the newest release: an unpinned install broke the
# debug image when Delve started requiring a newer Go than the base image had.
# v1.27.1 needs Go >= 1.25, which golang:1.26 satisfies.
RUN CGO_ENABLED=0 go install -ldflags "-s -w -extldflags '-static'" github.com/go-delve/delve/cmd/dlv@v1.27.1

FROM golang:1.27 as build-debug

WORKDIR /build
COPY go.mod go.sum ./
# cache the go mod download where possible
RUN go mod graph | awk '{if ($1 !~ "@") print $2}' | xargs go get

COPY . .
# CGO_ENABLED=0 creates a statically linked binary
# We need to have the symbols for debugging
RUN CGO_ENABLED=0 go build -gcflags "all=-N -l" -o "/bin/nfs-watchdog"

FROM golang:1.27 as build-prod

WORKDIR /build
COPY go.mod go.sum ./
# cache the go mod download where possible
RUN go mod graph | awk '{if ($1 !~ "@") print $2}' | xargs go get

COPY . .
# CGO_ENABLED=0 creates a statically linked binary
# -ldflags "-s -w" remove debug symbols
RUN env CGO_ENABLED=0 go build -ldflags "-s -w" -o "/bin/nfs-watchdog"

FROM scratch as debug
EXPOSE 40000
COPY --from=build-debug-base /go/bin/dlv /dlv
COPY --from=build-debug /bin/nfs-watchdog /bin/nfs-watchdog
ENTRYPOINT ["/dlv", "--listen=:40000", "--headless=true", "--api-version=2", "--accept-multiclient", "exec", "/bin/nfs-watchdog"]

FROM scratch as production
COPY --from=build-prod /bin/nfs-watchdog /bin/nfs-watchdog
ENTRYPOINT ["/bin/nfs-watchdog"]

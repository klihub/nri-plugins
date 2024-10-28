ARG GO_VERSION=1.24

FROM golang:${GO_VERSION}-bullseye AS builder

WORKDIR /go/builder

# Fetch go dependencies in a separate layer for caching
COPY go.mod go.sum ./
COPY pkg/topology/ pkg/topology/
RUN go mod download

# Build test-mem-hog
COPY . .

RUN make TEST_BINARIES=test-mem-hog IMAGE_VERSION=${IMAGE_VERSION} BUILD_VERSION=${BUILD_VERSION} BUILD_BUILDID=${BUILD_BUILDID} build-test-binaries-static

FROM gcr.io/distroless/static

COPY --from=builder /go/builder/build/bin/test-mem-hog /bin/test-mem-hog
#docker RUN ls -Fal /bin

ENTRYPOINT ["/bin/test-mem-hog"]

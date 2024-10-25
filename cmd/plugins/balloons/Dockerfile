ARG GO_VERSION=1.22

FROM golang:${GO_VERSION}-bullseye AS builder

ARG IMAGE_VERSION
ARG BUILD_VERSION
ARG BUILD_BUILDID
ARG DEBUG=0

WORKDIR /go/builder

# Fetch go dependencies in a separate layer for caching
COPY go.mod go.sum ./
COPY pkg/topology/ pkg/topology/
RUN go mod download

# Build nri-resource-policy
COPY . .

RUN make clean
RUN make IMAGE_VERSION=${IMAGE_VERSION} BUILD_VERSION=${BUILD_VERSION} BUILD_BUILDID=${BUILD_BUILDID} PLUGINS=nri-resource-policy-balloons DEBUG=$DEBUG V=$DEBUG build-plugins-static

RUN mkdir -p /debug-extras
RUN if [ "$DEBUG" = 1 ]; then \
        mkdir -p /debug-extras/bin; \
          GOBIN=/debug-extras/bin go install -tags osusergo,netgo -ldflags "-extldflags=-static" github.com/go-delve/delve/cmd/dlv@latest; \
        mkdir -p /debug-extras/go/builder; \
          mv /go/builder/pkg /debug-extras/go/builder; \
          mv /go/builder/cmd /debug-extras/go/builder; \
        mkdir -p /debug-extras/usr/local/go; \
          mv /go/pkg /debug-extras/go/pkg; \
        mkdir -p /debug-extras/usr/local/go; \
          mv /usr/local/go/src /debug-extras/usr/local/go; \
    fi

FROM gcr.io/distroless/static

COPY --from=builder /go/builder/build/bin/nri-resource-policy-balloons /bin/nri-resource-policy-balloons
COPY --from=builder /debug-extras /

ENTRYPOINT ["/bin/nri-resource-policy-balloons"]

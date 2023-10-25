# build env
FROM golang:1.20 AS build-env
COPY go.mod go.sum /src/
WORKDIR /src
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
ARG release=
RUN <<EOR
  VERSION=$(git rev-parse --short HEAD)
  BUILDTIME=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
  RELEASE=$release
  CGO_ENABLED=1 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /bin/dugtrio-proxy -ldflags="-s -w -X 'github.com/ethpandaops/dugtrio/utils.BuildVersion=${VERSION}' -X 'github.com/ethpandaops/dugtrio/utils.BuildRelease=${RELEASE}' -X 'github.com/ethpandaops/dugtrio/utils.BuildTime=${BUILDTIME}'" ./cmd/dugtrio-proxy
EOR

# final stage
FROM debian:stable-slim
WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates
RUN update-ca-certificates
COPY --from=build-env /bin/dugtrio-proxy /app
EXPOSE 8080
ENTRYPOINT ["./dugtrio-proxy"]
CMD []

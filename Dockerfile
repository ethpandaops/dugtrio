# build env
FROM golang:1.24 AS build-env
WORKDIR /src
COPY go.mod go.sum /src/
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
ARG release=
RUN <<EOR
  VERSION=$(git rev-parse --short HEAD)
  BUILDTIME=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
  RELEASE=$release
  go build -o bin/ -ldflags="-s -w -X 'github.com/ethpandaops/dugtrio/utils.BuildVersion=${VERSION}' -X 'github.com/ethpandaops/dugtrio/utils.BuildRelease=${RELEASE}' -X 'github.com/ethpandaops/dugtrio/utils.BuildTime=${BUILDTIME}'" ./cmd/dugtrio-proxy
EOR

# final stage
FROM debian:stable-slim
WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates
RUN update-ca-certificates
COPY --from=build-env /src/bin/* /app
EXPOSE 8080
ENTRYPOINT ["./dugtrio-proxy"]
CMD []

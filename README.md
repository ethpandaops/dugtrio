<img align="left" src="./.github/resources/dugtrio.png" width="90">
<h1>Dugtrio: Beaconchain Load Balancer</h1>

Dugtrio is a load balancing proxy for the ethereum beacon chain.

It supports various features:
* Close monitoring of connected endpoints to sort out forked off / unsynced clients
* Endpoint stickiness (Reuse the same endpoint for subsequent requests when possible)
* Client specific endpoints (client specific endpoints like `/lighthouse/...` or `/teku/...` are forwarded to the correct client type)
* Rate limiting per IP
* Path filtering (block certian endpoint paths)

## Getting Started

### Download a release
Download the latest release from the [Releases page](https://github.com/ethpandaops/dugtrio/releases). Extract and run with:

```
./dugtrio-proxy -config ./dugtrio-config.yaml
```

### Docker
Available as a docker image at [`ethpandaops/dugtrio`](https://hub.docker.com/r/ethpandaops/dugtrio)

Images
* `latest` - distroless, multiarch
* `debian-latest` - debian, multiarch
* `$version` - distroless, multiarch, pinned to a release (i.e. 1.0.1)
* `$version-debian` - debian, multiarch, pinned to a release (i.e. 1.0.1-debian)

### Build from source

Or build it yourself:

```
git clone https://github.com/ethpandaops/dugtrio.git
cd dugtrio
make
./bin/dugtrio-proxy -config ./dugtrio-config.yaml
```

## Usage

Dugtrio needs a configuration file with a list of client endpoints to use.
Create a copy of [dugtrio-config.example.yaml](https://github.com/ethpandaops/dugtrio/blob/master/dugtrio-config.example.yaml) and change it for your needs.

## Contact

pk910 - @pk910

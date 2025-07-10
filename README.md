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

## Header Fields for Client-Specific Routing

Dugtrio supports various header fields that enable you to specify which client endpoint should handle your request:

### Request Headers

**`X-Dugtrio-Next-Endpoint`**
- **Purpose**: Route requests to a specific beacon client type
- **Supported Values**: `lighthouse`, `lodestar`, `nimbus`, `prysm`, `teku`, `grandine`, `caplin`
- **Alternative**: Can also be specified as query parameter `dugtrio-next-endpoint`
- **Examples**: 
  ```bash
  # Using curl with header
  curl -H "X-Dugtrio-Next-Endpoint: lighthouse" https://your-dugtrio-proxy.com/eth/v1/node/version
  
  # Using query parameter instead
  curl https://your-dugtrio-proxy.com/eth/v1/node/version?dugtrio-next-endpoint=teku
  
  # Route to specific client for beacon state
  curl -H "X-Dugtrio-Next-Endpoint: nimbus" https://your-dugtrio-proxy.com/eth/v1/beacon/states/head/root
  ```

### Response Headers (Informational)

Dugtrio automatically adds these headers to responses for monitoring and debugging:

**`X-Dugtrio-Endpoint-Name`**
- Shows the name of the endpoint that processed the request

**`X-Dugtrio-Endpoint-Type`**
- Shows the client type that processed the request

**`X-Dugtrio-Endpoint-Version`**
- Shows the version of the endpoint that processed the request

**`X-Dugtrio-Session-Ip`**
- Shows the IP address of the session that made the request

**`X-Dugtrio-Session-Tokens`**
- Shows remaining rate limit tokens for the session

### Alternative Routing Methods

In addition to headers, you can also route to specific clients using URL prefixes:
- `/lighthouse/` - Routes to Lighthouse clients
- `/lodestar/` - Routes to Lodestar clients
- `/nimbus/` - Routes to Nimbus clients
- `/prysm/` - Routes to Prysm clients
- `/teku/` - Routes to Teku clients
- `/grandine/` - Routes to Grandine clients

## Contact

pk910 - @pk910

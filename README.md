<img align="left" src="./.github/resources/dugtrio.png" width="90">
<h1>Dugtrio: Beaconchain Load Balancer</h1>

Dugtrio is a load balancing proxy for the ethereum beacon chain.

It supports various features:

- Close monitoring of connected endpoints to sort out forked off / unsynced clients
- Multiple scheduler modes (round-robin and primary-fallback)
- Client specific endpoints (client specific endpoints like `/lighthouse/...`, `/teku/...`, or `/caplin/...` are forwarded to the correct client type)
- Rate limiting per IP
- Path filtering (block certian endpoint paths)

## Getting Started

### Download a release

Download the latest release from the [Releases page](https://github.com/ethpandaops/dugtrio/releases). Extract and run with:

```shell
./dugtrio-proxy -config ./dugtrio-config.yaml
```

### Docker

Available as a docker image at [`ethpandaops/dugtrio`](https://hub.docker.com/r/ethpandaops/dugtrio)

Images

- `latest` - distroless, multiarch
- `debian-latest` - debian, multiarch
- `$version` - distroless, multiarch, pinned to a release (i.e. 1.0.1)
- `$version-debian` - debian, multiarch, pinned to a release (i.e. 1.0.1-debian)

### Build from source

Or build it yourself:

```shell
git clone https://github.com/ethpandaops/dugtrio.git
cd dugtrio
make
./bin/dugtrio-proxy -config ./dugtrio-config.yaml
```

## Usage

Dugtrio needs a configuration file with a list of client endpoints to use.
Create a copy of [dugtrio-config.example.yaml](https://github.com/ethpandaops/dugtrio/blob/master/dugtrio-config.example.yaml) and change it for your needs.

## Scheduler Modes

Dugtrio supports two scheduler modes, configured via `pool.schedulerMode` in the config file.

### `rr` (round-robin, default)

Distributes requests across all ready endpoints in a round-robin fashion. Best for homogeneous setups where all endpoints are equivalent in data quality and latency.

### `primary-fallback`

Tries endpoints in **declaration order**. The first endpoint is always the primary — it handles all requests as long as it is healthy. Subsequent endpoints are only used if the primary returns a connection error, a non-2xx response, or an empty body.

```yaml
pool:
  schedulerMode: "primary-fallback"

endpoints:
  - name: local-beacon       # primary — always tried first
    url: "http://localhost:5052"
  - name: remote-fallback    # only used if local-beacon fails
    url: "http://fallback-beacon:5052"
```

**When to use it:** When your endpoints differ in data quality or cost. A common case is pairing a local full node (complete data, higher latency) with a managed RPC provider (fast but potentially incomplete responses). With round-robin, the fast-but-incomplete provider wins the race. With primary-fallback, the local node always serves requests and the provider is only used during outages.

## Header Fields for Client-Specific Routing

Dugtrio supports various header fields that enable you to specify which client endpoint should handle your request:

### Request Headers

**`X-Dugtrio-Next-Endpoint`**

- **Purpose**: Route requests to a specific beacon client type
- **Supported Values**: `lighthouse`, `lodestar`, `nimbus`, `prysm`, `teku`, `grandine`, `caplin` or individual client names
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

- `/caplin/` - Routes to Caplin clients
- `/grandine/` - Routes to Grandine clients
- `/lighthouse/` - Routes to Lighthouse clients
- `/lodestar/` - Routes to Lodestar clients
- `/nimbus/` - Routes to Nimbus clients
- `/prysm/` - Routes to Prysm clients
- `/teku/` - Routes to Teku clients

## Contact

pk910 - @pk910

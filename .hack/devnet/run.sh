#!/bin/bash
__dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [ -f "${__dir}/custom-kurtosis.devnet.config.yaml" ]; then
  config_file="${__dir}/custom-kurtosis.devnet.config.yaml"
else
  config_file="${__dir}/kurtosis.devnet.config.yaml"
fi

## Run devnet using kurtosis
ENCLAVE_NAME="${ENCLAVE_NAME:-dugtrio}"
ETHEREUM_PACKAGE="${ETHEREUM_PACKAGE:-github.com/ethpandaops/ethereum-package}"
if kurtosis enclave inspect "$ENCLAVE_NAME" > /dev/null; then
  echo "Kurtosis enclave '$ENCLAVE_NAME' is already up."
else
  kurtosis run "$ETHEREUM_PACKAGE" \
  --image-download always \
  --enclave "$ENCLAVE_NAME" \
  --args-file "${config_file}"
fi

## Generate Dugtrio config
ENCLAVE_UUID=$(kurtosis enclave inspect "$ENCLAVE_NAME" --full-uuids | grep 'UUID:' | awk '{print $2}')

BEACON_NODES=$(docker ps -aq -f "label=kurtosis_enclave_uuid=$ENCLAVE_UUID" \
              -f "label=com.kurtosistech.app-id=kurtosis" \
              -f "label=com.kurtosistech.custom.ethereum-package.client-type=beacon" | tac)

cat <<EOF > "${__dir}/generated-dugtrio-config.yaml"
logging:
  outputLevel: "info"

server:
  host: "0.0.0.0"
  port: "8080"

endpoints:
EOF

# Add beacon endpoints
for node in $BEACON_NODES; do
    name=$(docker inspect -f "{{ with index .Config.Labels \"com.kurtosistech.id\"}}{{.}}{{end}}" $node)
    client_type=$(docker inspect -f "{{ with index .Config.Labels \"com.kurtosistech.custom.ethereum-package.cl-client-type\"}}{{.}}{{end}}" $node)
    ip=$(echo '127.0.0.1')
    
    # Try different beacon ports based on client type
    port=""
    case $client_type in
        lighthouse)
            port=$(docker inspect --format='{{ (index (index .NetworkSettings.Ports "5052/tcp") 0).HostPort }}' $node 2>/dev/null)
            ;;
        prysm)
            port=$(docker inspect --format='{{ (index (index .NetworkSettings.Ports "3500/tcp") 0).HostPort }}' $node 2>/dev/null)
            ;;
        teku)
            port=$(docker inspect --format='{{ (index (index .NetworkSettings.Ports "5051/tcp") 0).HostPort }}' $node 2>/dev/null)
            ;;
        nimbus)
            port=$(docker inspect --format='{{ (index (index .NetworkSettings.Ports "5052/tcp") 0).HostPort }}' $node 2>/dev/null)
            ;;
        lodestar)
            port=$(docker inspect --format='{{ (index (index .NetworkSettings.Ports "9596/tcp") 0).HostPort }}' $node 2>/dev/null)
            ;;
        grandine)
            port=$(docker inspect --format='{{ (index (index .NetworkSettings.Ports "5052/tcp") 0).HostPort }}' $node 2>/dev/null)
            ;;
        *)
            # Default fallback - try common beacon ports
            port=$(docker inspect --format='{{ (index (index .NetworkSettings.Ports "5052/tcp") 0).HostPort }}' $node 2>/dev/null)
            if [ -z "$port" ]; then
                port=$(docker inspect --format='{{ (index (index .NetworkSettings.Ports "3500/tcp") 0).HostPort }}' $node 2>/dev/null)
            fi
            if [ -z "$port" ]; then
                port=$(docker inspect --format='{{ (index (index .NetworkSettings.Ports "5051/tcp") 0).HostPort }}' $node 2>/dev/null)
            fi
            ;;
    esac
    
    if [ -z "$port" ]; then
        echo "Warning: Could not find beacon port for $name ($client_type), skipping..."
        continue
    fi
    
    echo "  - name: \"$name\"" >> "${__dir}/generated-dugtrio-config.yaml"
    echo "    url: \"http://$ip:$port\"" >> "${__dir}/generated-dugtrio-config.yaml"
done

cat <<EOF >> "${__dir}/generated-dugtrio-config.yaml"

pool:
  schedulerMode: "rr"
  followDistance: 10
  maxHeadDistance: 2

proxy:
  proxyCount: 0
  callTimeout: 60s
  sessionTimeout: 10m
  stickyEndpoint: true
  callRateLimit: 100
  callRateBurst: 1000
  blockedPaths:
    - ^/eth/v[0-9]+/debug/.*
  authorization:
    require: false
    password: ""
  rebalanceInterval: 10s
  rebalanceThreshold: 0.1
  rebalanceAbsThreshold: 3
  rebalanceMaxSweep: 10

frontend:
  enabled: true
  minify: false
  siteName: "Dugtrio Devnet"
  pprof: true

metrics:
  enabled: true
  host: "0.0.0.0"
  port: "9090"
EOF

cat <<EOF
============================================================================================================
Dugtrio devnet is ready!

Configuration file: ${__dir}/generated-dugtrio-config.yaml

To start dugtrio:
  make devnet-run

Or manually:
  go run cmd/dugtrio-proxy/main.go --config ${__dir}/generated-dugtrio-config.yaml

Health dashboard: http://localhost:8080/health
Metrics: http://localhost:9090/metrics

Endpoints configured:
EOF

# List the configured endpoints
grep -A 1 "name:" "${__dir}/generated-dugtrio-config.yaml" | sed 's/^/  /'

echo "============================================================================================================"
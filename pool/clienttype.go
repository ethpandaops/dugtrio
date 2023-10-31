package pool

import "regexp"

type ClientType int8

var (
	UnspecifiedClient ClientType = 0
	UnknownClient     ClientType = -1
	LighthouseClient  ClientType = 1
	LodestarClient    ClientType = 2
	NimbusClient      ClientType = 3
	PrysmClient       ClientType = 4
	TekuClient        ClientType = 5
)
var clientTypePatterns = map[ClientType]*regexp.Regexp{
	LighthouseClient: regexp.MustCompile("(?i)^Lighthouse/.*"),
	LodestarClient:   regexp.MustCompile("(?i)^Lodestar/.*"),
	NimbusClient:     regexp.MustCompile("(?i)^Nimbus/.*"),
	PrysmClient:      regexp.MustCompile("(?i)^Prysm/.*"),
	TekuClient:       regexp.MustCompile("(?i)^teku/.*"),
}

func (client *PoolClient) parseClientVersion(version string) {
	for clientType, versionPattern := range clientTypePatterns {
		if versionPattern.Match([]byte(version)) {
			client.clientType = clientType
			return
		}
	}
	client.clientType = UnknownClient
}

func (client *PoolClient) GetClientType() ClientType {
	return client.clientType
}

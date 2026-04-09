package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestProxyConfig_ClientsUnmarshal(t *testing.T) {
	raw := `
proxy:
  require_tokens: true
  clients:
    - token: "abc123"
      name: "Acme Corp"
      billing_code: "ACM-ETH-01"
    - token: "xyz789"
      name: "Globex"
      billing_code: "GLB-ETH-02"
`
	cfg := &Config{}
	err := yaml.Unmarshal([]byte(raw), cfg)
	require.NoError(t, err)
	require.NotNil(t, cfg.Proxy)
	assert.True(t, cfg.Proxy.RequireTokens)
	require.Len(t, cfg.Proxy.Clients, 2)
	assert.Equal(t, "abc123", cfg.Proxy.Clients[0].Token)
	assert.Equal(t, "Acme Corp", cfg.Proxy.Clients[0].Name)
	assert.Equal(t, "ACM-ETH-01", cfg.Proxy.Clients[0].BillingCode)
	assert.Equal(t, "xyz789", cfg.Proxy.Clients[1].Token)
	assert.Equal(t, "GLB-ETH-02", cfg.Proxy.Clients[1].BillingCode)
}

func TestProxyConfig_DefaultsWhenClientsAbsent(t *testing.T) {
	raw := `proxy: {}`
	cfg := &Config{}
	err := yaml.Unmarshal([]byte(raw), cfg)
	require.NoError(t, err)
	require.NotNil(t, cfg.Proxy)
	assert.False(t, cfg.Proxy.RequireTokens)
	assert.Empty(t, cfg.Proxy.Clients)
}

package proxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizePath(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"/eth/v1/node/version", "/eth/v1/node/version"},
		{"/eth/v1/beacon/blocks/12345", "/eth/v1/beacon/blocks/{id}"},
		{"/eth/v1/beacon/headers/0xabc123def", "/eth/v1/beacon/headers/{hex}"},
		{"/eth/v1/beacon/blob_sidecars/7890", "/eth/v1/beacon/blob_sidecars/{id}"},
		{"/eth/v1/beacon/states/head/finality_checkpoints", "/eth/v1/beacon/states/head/finality_checkpoints"},
		{"/eth/v1/beacon/states/99/validators/0xabc", "/eth/v1/beacon/states/{id}/validators/{hex}"},
		{"/eth/v1/validator/duties/attester/5", "/eth/v1/validator/duties/attester/{id}"},
		{"/eth/v1/beacon/blocks/12345?format=ssz", "/eth/v1/beacon/blocks/{id}"},
		{"/eth/v1/beacon/blocks/head", "/eth/v1/beacon/blocks/head"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.expected, NormalizePath(tc.input))
		})
	}
}

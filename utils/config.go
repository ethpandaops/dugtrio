package utils

import (
	"fmt"
	"os"

	"github.com/kelseyhightower/envconfig"
	"gopkg.in/yaml.v3"

	"github.com/ethpandaops/dugtrio/types"
)

// ReadConfig will process a configuration
func ReadConfig(cfg *types.Config, path string) error {
	err := readConfigFile(cfg, path)
	if err != nil {
		return err
	}

	err = readConfigEnv(cfg)
	if err != nil {
		return err
	}

	if len(cfg.Endpoints) == 0 {
		return fmt.Errorf("missing beacon node endpoints (need at least 1 endpoint)")
	}

	return nil
}

func readConfigFile(cfg *types.Config, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("error opening config file %v: %v", path, err)
	}
	defer f.Close()

	decoder := yaml.NewDecoder(f)

	err = decoder.Decode(cfg)
	if err != nil {
		return fmt.Errorf("error decoding explorer config: %v", err)
	}

	return nil
}

func readConfigEnv(cfg *types.Config) error {
	return envconfig.Process("", cfg)
}

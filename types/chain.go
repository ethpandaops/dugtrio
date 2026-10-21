package types

import (
	"reflect"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/mashingan/smapping"
)

const FarFutureEpoch = uint64(18446744073709551615)

type ForkVersion struct {
	Epoch           uint64
	CurrentVersion  []byte
	PreviousVersion []byte
}

// https://github.com/ethereum/consensus-specs/blob/dev/configs/mainnet.yaml
type ChainConfig struct {
	PresetBase           string         `yaml:"PRESET_BASE"`
	ConfigName           string         `yaml:"CONFIG_NAME"`
	MinGenesisTime       time.Time      `yaml:"MIN_GENESIS_TIME"`
	GenesisForkVersion   phase0.Version `yaml:"GENESIS_FORK_VERSION"`
	AltairForkVersion    phase0.Version `yaml:"ALTAIR_FORK_VERSION"`
	AltairForkEpoch      uint64         `yaml:"ALTAIR_FORK_EPOCH"`
	BellatrixForkVersion phase0.Version `yaml:"BELLATRIX_FORK_VERSION"`
	BellatrixForkEpoch   uint64         `yaml:"BELLATRIX_FORK_EPOCH"`
	CappellaForkVersion  phase0.Version `yaml:"CAPELLA_FORK_VERSION"`
	CappellaForkEpoch    uint64         `yaml:"CAPELLA_FORK_EPOCH"`
	HezeForkEpoch        uint64         `yaml:"HEZE_FORK_EPOCH" check-if-fork:"HezeForkEpoch"`
	SlotDurationMs       uint64         `yaml:"SLOT_DURATION_MS"`
	SlotDurationMsHeze   uint64         `yaml:"SLOT_DURATION_MS_HEZE" check-if-fork:"HezeForkEpoch"`
	SlotsPerEpoch        uint64         `yaml:"SLOTS_PER_EPOCH"`
}

func ParseChainConfig(specValues map[string]interface{}) (*ChainConfig, error) {
	specs := &ChainConfig{}

	err := smapping.FillStructByTags(specs, specValues, "yaml")
	if err != nil {
		return nil, err
	}

	if _, ok := specValues["HEZE_FORK_EPOCH"]; !ok {
		specs.HezeForkEpoch = FarFutureEpoch
	}

	if specs.SlotDurationMs == 0 {
		if secondsPerSlot, ok := specValues["SECONDS_PER_SLOT"].(time.Duration); ok {
			specs.SlotDurationMs = uint64(secondsPerSlot.Milliseconds()) //nolint:gosec // no overflow possible
		}
	}

	return specs, nil
}

func (chain *ChainConfig) CheckMismatch(chain2 *ChainConfig) []string {
	mismatches := []string{}
	chainT := reflect.ValueOf(chain).Elem()
	chain2T := reflect.ValueOf(chain2).Elem()

	for i := 0; i < chainT.NumField(); i++ {
		if checkIfFork := chainT.Type().Field(i).Tag.Get("check-if-fork"); checkIfFork != "" {
			if chainT.FieldByName(checkIfFork).Uint() == FarFutureEpoch && chain2T.FieldByName(checkIfFork).Uint() == FarFutureEpoch {
				continue
			}
		}

		if chainT.Field(i).Interface() != chain2T.Field(i).Interface() {
			mismatches = append(mismatches, chainT.Type().Field(i).Name)
		}
	}

	return mismatches
}

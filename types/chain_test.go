package types

import (
	"testing"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
)

func devnet3SpecValues() map[string]interface{} {
	return map[string]interface{}{
		"CONFIG_NAME":           "heze-devnet-3",
		"SECONDS_PER_SLOT":      12 * time.Second,
		"SLOT_DURATION_MS":      uint64(12000),
		"SLOTS_PER_EPOCH":       uint64(32),
		"HEZE_FORK_EPOCH":       uint64(5),
		"SLOT_DURATION_MS_HEZE": uint64(9000),
	}
}

func TestParseChainConfig(t *testing.T) {
	specs, err := ParseChainConfig(devnet3SpecValues())
	if err != nil {
		t.Fatal(err)
	}

	if specs.SlotDurationMs != 12000 || specs.HezeForkEpoch != 5 || specs.SlotDurationMsHeze != 9000 {
		t.Fatalf("unexpected specs: %+v", specs)
	}

	specs, err = ParseChainConfig(map[string]interface{}{
		"SECONDS_PER_SLOT": 6 * time.Second,
		"SLOTS_PER_EPOCH":  uint64(32),
	})
	if err != nil {
		t.Fatal(err)
	}

	if specs.SlotDurationMs != 6000 {
		t.Fatalf("expected SECONDS_PER_SLOT fallback to 6000, got %v", specs.SlotDurationMs)
	}

	if specs.HezeForkEpoch != FarFutureEpoch {
		t.Fatalf("expected missing HEZE_FORK_EPOCH to be far future, got %v", specs.HezeForkEpoch)
	}
}

func TestGetSlotDurationMs(t *testing.T) {
	devnet3, err := ParseChainConfig(devnet3SpecValues())
	if err != nil {
		t.Fatal(err)
	}

	unchanged := *devnet3
	unchanged.SlotDurationMsHeze = 12000

	farFuture := *devnet3
	farFuture.HezeForkEpoch = FarFutureEpoch

	noHezeDuration := *devnet3
	noHezeDuration.SlotDurationMsHeze = 0

	tests := []struct {
		name     string
		specs    *ChainConfig
		epoch    phase0.Epoch
		expected uint64
	}{
		{"devnet3 before fork", devnet3, 4, 12000},
		{"devnet3 at fork", devnet3, 5, 9000},
		{"devnet3 after fork", devnet3, 1000, 9000},
		{"unchanged duration", &unchanged, 1000, 12000},
		{"far future fork", &farFuture, phase0.Epoch(FarFutureEpoch - 1), 12000},
		{"missing heze duration", &noHezeDuration, 1000, 12000},
	}

	for _, test := range tests {
		if duration := test.specs.GetSlotDurationMs(test.epoch); duration != test.expected {
			t.Errorf("%v: expected %v, got %v", test.name, test.expected, duration)
		}
	}
}

func TestCheckMismatch(t *testing.T) {
	devnet3, err := ParseChainConfig(devnet3SpecValues())
	if err != nil {
		t.Fatal(err)
	}

	otherHezeDuration := *devnet3
	otherHezeDuration.SlotDurationMsHeze = 6000

	if mismatches := devnet3.CheckMismatch(&otherHezeDuration); len(mismatches) != 1 || mismatches[0] != "SlotDurationMsHeze" {
		t.Errorf("expected SlotDurationMsHeze mismatch, got %v", mismatches)
	}

	unscheduled := *devnet3
	unscheduled.HezeForkEpoch = FarFutureEpoch
	unscheduled.SlotDurationMsHeze = 0

	if mismatches := devnet3.CheckMismatch(&unscheduled); len(mismatches) != 2 {
		t.Errorf("expected HezeForkEpoch and SlotDurationMsHeze mismatches, got %v", mismatches)
	}

	unscheduledOther := unscheduled
	unscheduledOther.SlotDurationMsHeze = 9000

	if mismatches := unscheduled.CheckMismatch(&unscheduledOther); len(mismatches) != 0 {
		t.Errorf("expected no mismatches while heze is unscheduled, got %v", mismatches)
	}
}

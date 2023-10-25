package pool

type BeaconPool struct {
}

func NewBeaconPool() (*BeaconPool, error) {
	pool := BeaconPool{}

	return &pool, nil
}

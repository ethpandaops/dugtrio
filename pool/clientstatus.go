package pool

type ClientStatus uint8

var (
	ClientStatusOnline        ClientStatus = 1
	ClientStatusOffline       ClientStatus = 2
	ClientStatusSynchronizing ClientStatus = 3
	ClientStatusOptimistic    ClientStatus = 4
)

func (status ClientStatus) String() string {
	switch status {
	case ClientStatusOnline:
		return "Online"
	case ClientStatusOffline:
		return "Offline"
	case ClientStatusSynchronizing:
		return "Synchronizing"
	case ClientStatusOptimistic:
		return "Optimistic"
	default:
		return "Unknown"
	}
}

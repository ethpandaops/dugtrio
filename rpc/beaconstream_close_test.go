package rpc

import (
	"testing"
	"time"
)

type fakeBlockEvent struct {
	data string
}

func (e fakeBlockEvent) Id() string    { return "" }
func (e fakeBlockEvent) Event() string { return "block" }
func (e fakeBlockEvent) Data() string  { return e.data }
func (e fakeBlockEvent) Retry() int64  { return 0 }

// TestCloseDoesNotDeadlockOnFullEventChan reproduces the exact shape of the previous
// deadlock: a producer holds runMutex (as startStream does for its whole lifetime) and
// is parked trying to push an event onto a full EventChan that nothing drains. Close
// must still be able to return instead of blocking forever on runMutex.
func TestCloseDoesNotDeadlockOnFullEventChan(t *testing.T) {
	root := "0x0000000000000000000000000000000000000000000000000000000000000000"
	evt := fakeBlockEvent{data: `{"slot":"1","block":"` + root + `","execution_optimistic":false}`}

	bs := &BeaconStream{
		running:   true,
		killChan:  make(chan bool),
		ReadyChan: make(chan bool, 10),
		EventChan: make(chan *BeaconStreamEvent, 10),
	}

	producerBlocked := make(chan struct{})

	go func() {
		bs.runMutex.Lock()
		defer bs.runMutex.Unlock()

		// Fill the buffer directly, then call the real production method for the
		// send that would have blocked forever before the fix.
		for i := 0; i < 10; i++ {
			bs.EventChan <- &BeaconStreamEvent{Event: StreamBlockEvent}
		}

		close(producerBlocked)

		bs.processBlockEvent(evt)
	}()

	<-producerBlocked
	time.Sleep(100 * time.Millisecond) // let processBlockEvent reach its send

	closeDone := make(chan struct{})

	go func() {
		bs.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return: still deadlocked on a full EventChan")
	}
}

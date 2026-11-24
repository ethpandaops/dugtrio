package eventstream

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestCloseDuringInFlightSend closes the stream at the moment an event is being
// delivered to a caller that never reads from Events. Before the fix, this could hit a
// send on a channel that Close had already closed, panicking the whole process. Now
// Close only signals shutdown through a dedicated channel and never closes Events or
// Errors, so a racing send always has a safe way out.
func TestCloseDuringInFlightSend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"n\":1}\n\n")

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		<-r.Context().Done()
	}))
	defer srv.CloseClientConnections()

	stream, err := Subscribe(srv.URL, "")
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	<-stream.Ready

	// Nothing reads from stream.Events, so the pending send is still parked when
	// Close runs below - this is the exact interleaving that used to panic.
	time.Sleep(200 * time.Millisecond)

	stream.Close()

	time.Sleep(300 * time.Millisecond)
}

// TestCloseIsIdempotent confirms repeated Close calls, including concurrent ones,
// never panic or block.
func TestCloseIsIdempotent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		<-r.Context().Done()
	}))
	defer srv.CloseClientConnections()

	stream, err := Subscribe(srv.URL, "")
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	<-stream.Ready

	done := make(chan struct{})

	for i := 0; i < 5; i++ {
		go func() {
			stream.Close()
			done <- struct{}{}
		}()
	}

	for i := 0; i < 5; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Close did not return")
		}
	}
}

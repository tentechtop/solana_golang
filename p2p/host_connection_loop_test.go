package p2p

import "testing"

func TestConnectionMessageSequencerKeepsOrderedDispatch(t *testing.T) {
	first := Message{ID: "first"}
	second := Message{ID: "second"}
	dispatched := make([]string, 0, 2)
	sequencer := newConnectionMessageSequencer(func(message Message) connectionStop {
		dispatched = append(dispatched, message.ID)
		return connectionStop{}
	})

	sequencer.dispatch(connectionParseJob{orderKey: "peer/12", orderSeq: 1}, second)
	if len(dispatched) != 0 {
		t.Fatalf("dispatched = %v, want no dispatch before missing first sequence", dispatched)
	}
	sequencer.dispatch(connectionParseJob{orderKey: "peer/12", orderSeq: 0}, first)
	if len(dispatched) != 2 || dispatched[0] != "first" || dispatched[1] != "second" {
		t.Fatalf("dispatched = %v, want first then second", dispatched)
	}
}

func TestConnectionMessageSequencerDispatchesStatelessImmediately(t *testing.T) {
	message := Message{ID: "stateless"}
	dispatched := make(chan string, 1)
	sequencer := newConnectionMessageSequencer(func(message Message) connectionStop {
		dispatched <- message.ID
		return connectionStop{}
	})

	sequencer.dispatch(connectionParseJob{}, message)
	select {
	case got := <-dispatched:
		if got != message.ID {
			t.Fatalf("dispatched id = %q, want %q", got, message.ID)
		}
	default:
		t.Fatal("stateless message was not dispatched immediately")
	}
}

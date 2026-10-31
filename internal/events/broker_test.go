package events_test

import (
	"testing"

	"github.com/bzumhagen/idrometer/internal/domain"
	"github.com/bzumhagen/idrometer/internal/events"
)

func TestFanOutAndCancel(t *testing.T) {
	b := events.NewBroker()
	a, cancelA := b.Subscribe()
	c, cancelC := b.Subscribe()
	defer cancelC()

	b.Publish(domain.Event{Type: domain.EventReading, FermentationID: 7})
	if e := <-a; e.FermentationID != 7 {
		t.Fatalf("a got %+v", e)
	}
	if e := <-c; e.FermentationID != 7 {
		t.Fatalf("c got %+v", e)
	}
	cancelA()
	cancelA() // idempotent
	if _, open := <-a; open {
		t.Fatal("channel should be closed after cancel")
	}
	if b.Subscribers() != 1 {
		t.Fatalf("subscribers = %d", b.Subscribers())
	}
}

func TestSlowSubscriberIsDroppedNotBlocking(t *testing.T) {
	b := events.NewBroker()
	slow, _ := b.Subscribe()
	for i := 0; i < 1000; i++ { // never read; publisher must not block
		b.Publish(domain.Event{FermentationID: int64(i)})
	}
	n := 0
	for range slow {
		n++
	}
	if n == 0 || n > 64 || b.Subscribers() != 0 {
		t.Fatalf("drained %d events, subscribers %d", n, b.Subscribers())
	}
}

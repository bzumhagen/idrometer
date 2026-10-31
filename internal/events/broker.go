// Package events is an in-process pub/sub broker. It implements
// domain.EventPublisher and feeds the SSE endpoints.
package events

import (
	"sync"

	"github.com/bzumhagen/idrometer/internal/domain"
)

const buffer = 64

// Broker fans events out to subscribers. A subscriber that falls behind has
// its channel closed rather than blocking the publisher; the client simply
// reconnects and reloads.
type Broker struct {
	mu   sync.Mutex
	subs map[chan domain.Event]struct{}
}

var _ domain.EventPublisher = (*Broker)(nil)

func NewBroker() *Broker { return &Broker{subs: map[chan domain.Event]struct{}{}} }

// Subscribe returns a channel of events and a function that ends the
// subscription. The channel is closed on cancel or when the subscriber lags.
func (b *Broker) Subscribe() (<-chan domain.Event, func()) {
	ch := make(chan domain.Event, buffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() { b.drop(ch) }
}

func (b *Broker) drop(ch chan domain.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[ch]; ok {
		delete(b.subs, ch)
		close(ch)
	}
}

func (b *Broker) Publish(e domain.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
			delete(b.subs, ch)
			close(ch)
		}
	}
}

// Subscribers reports the current subscriber count (for tests and metrics).
func (b *Broker) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

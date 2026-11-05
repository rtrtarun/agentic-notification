// Package stream fans small "something changed" messages out to connected
// browsers. It carries no notification content: a message only tells the
// console to refresh, so what a person may see is still decided by the API.
package stream

import (
	"errors"
	"sync"
)

// ErrTooManySubscribers is returned when the connection cap is reached.
var ErrTooManySubscribers = errors.New("too many live connections")

// Message types.
const (
	// TypeReady is sent once when a client connects.
	TypeReady = "ready"
	// TypeEvent means a new event was processed.
	TypeEvent = "event"
	// TypeInbox means notification state changed (read, archived, rules and so on).
	TypeInbox = "inbox"
)

// Message is one update. ID is set for TypeEvent.
type Message struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
	// AttentionClass is set for TypeEvent so the console can tell a critical arrival apart.
	AttentionClass string `json:"attentionClass,omitempty"`
}

// bufferSize is how many undelivered messages one slow client may hold.
const bufferSize = 16

// Broker delivers messages to every subscriber without ever blocking a publisher.
type Broker struct {
	mu   sync.Mutex
	max  int
	subs map[*Subscription]struct{}
}

// NewBroker caps concurrent subscribers at max; zero or less means 100.
func NewBroker(max int) *Broker {
	if max <= 0 {
		max = 100
	}
	return &Broker{max: max, subs: map[*Subscription]struct{}{}}
}

// Subscription receives messages on C until Close is called.
type Subscription struct {
	C      chan Message
	broker *Broker
	once   sync.Once
}

// Subscribe registers a client.
func (b *Broker) Subscribe() (*Subscription, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.subs) >= b.max {
		return nil, ErrTooManySubscribers
	}
	s := &Subscription{C: make(chan Message, bufferSize), broker: b}
	b.subs[s] = struct{}{}
	return s, nil
}

// Close unregisters the client. It is safe to call more than once.
func (s *Subscription) Close() {
	s.once.Do(func() {
		s.broker.mu.Lock()
		delete(s.broker.subs, s)
		s.broker.mu.Unlock()
	})
}

// Count returns how many clients are connected.
func (b *Broker) Count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

// Publish sends m to every subscriber. A client whose buffer is full loses its
// oldest message instead of holding up the publisher; every message is only a
// cue to refresh, so a lost one costs nothing once a later one arrives.
func (b *Broker) Publish(m Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subs {
		select {
		case s.C <- m:
		default:
			select {
			case <-s.C:
			default:
			}
			select {
			case s.C <- m:
			default:
			}
		}
	}
}

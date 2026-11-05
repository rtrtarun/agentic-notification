package stream_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/example/notification-service/pkg/stream"
)

func recv(t *testing.T, s *stream.Subscription) stream.Message {
	t.Helper()
	select {
	case m := <-s.C:
		return m
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a message")
		return stream.Message{}
	}
}

func TestPublishReachesEverySubscriber(t *testing.T) {
	b := stream.NewBroker(10)
	a, _ := b.Subscribe()
	c, _ := b.Subscribe()
	defer a.Close()
	defer c.Close()
	b.Publish(stream.Message{Type: stream.TypeEvent, ID: "e1", AttentionClass: "critical"})
	for _, s := range []*stream.Subscription{a, c} {
		if m := recv(t, s); m.ID != "e1" || m.AttentionClass != "critical" {
			t.Fatalf("got %+v", m)
		}
	}
}

func TestCloseStopsDeliveryAndIsIdempotent(t *testing.T) {
	b := stream.NewBroker(10)
	s, _ := b.Subscribe()
	s.Close()
	s.Close()
	if b.Count() != 0 {
		t.Fatalf("count = %d", b.Count())
	}
	b.Publish(stream.Message{Type: stream.TypeInbox})
	select {
	case <-s.C:
		t.Fatal("closed subscription received a message")
	default:
	}
}

func TestSubscriberCap(t *testing.T) {
	b := stream.NewBroker(2)
	s1, _ := b.Subscribe()
	if _, err := b.Subscribe(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Subscribe(); !errors.Is(err, stream.ErrTooManySubscribers) {
		t.Fatalf("third subscribe = %v", err)
	}
	s1.Close()
	if _, err := b.Subscribe(); err != nil {
		t.Fatalf("slot was not freed: %v", err)
	}
}

func TestSlowSubscriberNeverBlocksAndKeepsTheNewest(t *testing.T) {
	b := stream.NewBroker(10)
	slow, _ := b.Subscribe()
	defer slow.Close()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			b.Publish(stream.Message{Type: stream.TypeEvent, ID: "x"})
		}
		b.Publish(stream.Message{Type: stream.TypeInbox, ID: "last"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publisher blocked on a slow subscriber")
	}
	var last stream.Message
	for len(slow.C) > 0 {
		last = <-slow.C
	}
	if last.ID != "last" {
		t.Fatalf("newest message was lost: %+v", last)
	}
}

func TestConcurrentUseIsSafe(t *testing.T) {
	b := stream.NewBroker(1000)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			s, err := b.Subscribe()
			if err != nil {
				return
			}
			s.Close()
		}()
		go func() {
			defer wg.Done()
			b.Publish(stream.Message{Type: stream.TypeEvent})
		}()
	}
	wg.Wait()
}

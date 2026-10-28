package camera

import (
	"context"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/nvr"
)

func TestHubFansOut(t *testing.T) {
	src := make(chan nvr.Event)
	h := NewHub()
	a, cancelA := h.Subscribe()
	b, cancelB := h.Subscribe()
	defer cancelB()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Run(ctx, func(context.Context, []int) (<-chan nvr.Event, <-chan error) { return src, nil }, []int{1})
	src <- nvr.Event{Channel: 1, Kind: "motion", Active: true}
	for _, c := range []<-chan nvr.Event{a, b} {
		select {
		case ev := <-c:
			if ev.Channel != 1 || ev.Kind != "motion" {
				t.Fatal(ev)
			}
		case <-time.After(time.Second):
			t.Fatal("no llegó el evento")
		}
	}
	cancelA()
	cancelA() // idempotente
	if h.Subscribers() != 1 {
		t.Fatal(h.Subscribers())
	}
}

func TestHubSlowSubscriberDoesNotBlock(t *testing.T) {
	h := NewHub()
	_, cancelSlow := h.Subscribe()
	defer cancelSlow()
	for i := 0; i < 100; i++ {
		h.publish(nvr.Event{Channel: 1})
	}
}

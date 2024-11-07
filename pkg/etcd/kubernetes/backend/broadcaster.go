package backend

import (
	"context"
	"sync"
)

type broadcaster struct {
	sync.Mutex

	subs map[chan interface{}]struct{}
}

func (b *broadcaster) Subscribe(ctx context.Context) <-chan interface{} {
	b.Lock()
	defer b.Unlock()

	sub := make(chan interface{}, 100)
	if b.subs == nil {
		b.subs = map[chan interface{}]struct{}{}
	}
	b.subs[sub] = struct{}{}
	go func() {
		<-ctx.Done()
		b.unsub(sub, true)
	}()

	return sub
}

func (b *broadcaster) Stream(item interface{}) {
	b.Lock()
	for sub := range b.subs {
		select {
		case sub <- item:
		default:
			// Slow consumer, drop
			go b.unsub(sub, true)
		}
	}
	b.Unlock()
}

func (b *broadcaster) unsub(sub chan interface{}, lock bool) {
	if lock {
		b.Lock()
	}
	if _, ok := b.subs[sub]; ok {
		close(sub)
		delete(b.subs, sub)
	}
	if lock {
		b.Unlock()
	}
}

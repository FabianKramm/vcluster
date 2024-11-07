package backend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/loft-sh/vcluster/pkg/etcd/kubernetes/server"
	"github.com/sirupsen/logrus"
	"github.com/tidwall/btree"
	"k8s.io/klog/v2"
)

func NewBackend(storage Storage) server.Backend {
	return &backend{
		tracing: false,

		storage: storage,

		broadcaster: &broadcaster{},

		byKeys: btree.NewBTreeG[*server.Event](func(a, b *server.Event) bool {
			if a.KV.Key == b.KV.Key {
				return a.KV.ModRevision < b.KV.ModRevision
			}

			return a.KV.Key < b.KV.Key
		}),
	}
}

type backend struct {
	m sync.RWMutex

	tracing bool

	storage Storage

	broadcaster *broadcaster

	lastCompacted   int64
	currentRevision int64

	byKeys *btree.BTreeG[*server.Event]
}

func (b *backend) trace(ctx context.Context, msg string, keysAndValues ...interface{}) {
	if !b.tracing {
		return
	}

	klog.FromContext(ctx).V(1).Info(msg, keysAndValues...)
}

func (b *backend) Start(ctx context.Context) error {
	err := b.storage.Start(ctx)
	if err != nil {
		return fmt.Errorf("starting storage: %w", err)
	}

	rows, err := b.storage.List(ctx)
	if err != nil {
		return fmt.Errorf("listing rows: %w", err)
	}

	b.currentRevision = 0
	for _, row := range rows {
		if row.KV.ModRevision > b.currentRevision {
			b.currentRevision = row.KV.ModRevision
		}

		b.byKeys.Set(row)
	}

	// See https://github.com/kubernetes/kubernetes/blob/442a69c3bdf6fe8e525b05887e57d89db1e2f3a5/staging/src/k8s.io/apiserver/pkg/storage/storagebackend/factory/etcd3.go#L97
	if _, err := b.Create(ctx, "/registry/health", []byte(`{"health":"true"}`), 0); err != nil {
		if !errors.Is(err, server.ErrKeyExists) {
			logrus.Errorf("Failed to create health check key: %v", err)
		}
	}

	go func() {
		myTimer := time.NewTicker(time.Second * 10)
		defer myTimer.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-myTimer.C:
				dbSize, _ := b.DbSize(ctx)
				fmt.Println(dbSize/1024, "KB")
				b.m.RLock()
				fmt.Println(b.byKeys.Len())
				b.m.RUnlock()
			}
		}
	}()

	return nil
}

func (b *backend) Get(ctx context.Context, prefix string, revision int64) (revRet int64, retKV *server.KeyValue, errRet error) {
	defer func() {
		b.trace(ctx, "GET", "prefix", prefix, "revision", revision, "rev", revRet, "kv", retKV != nil, "err", errRet)
	}()

	b.m.RLock()
	defer b.m.RUnlock()

	rev, row, err := b.getLatest(prefix, revision, false)
	if err != nil {
		return 0, nil, err
	}
	if row == nil {
		return rev, nil, nil
	}

	return rev, row.KV, nil
}

func (b *backend) Create(ctx context.Context, key string, value []byte, lease int64) (revRet int64, errRet error) {
	defer func() {
		b.trace(ctx, "CREATE", "key", key, "size", len(value), "lease", lease, "ret", revRet, "err", errRet)
	}()

	b.m.Lock()
	defer b.m.Unlock()

	rev, prevEvent, err := b.getLatest(key, 0, true)
	if err != nil {
		return rev, err
	}

	newRevision := b.nextRevision()
	newRow := &server.Event{
		Create: true,
		KV: &server.KeyValue{
			Key:            key,
			Value:          value,
			CreateRevision: newRevision,
			ModRevision:    newRevision,
			Lease:          lease,
		},
		PrevKV: &server.KeyValue{
			ModRevision: rev,
		},
	}
	if prevEvent != nil {
		if !prevEvent.Delete {
			return rev, server.ErrKeyExists
		}
		newRow.PrevKV = prevEvent.KV
	}
	err = b.storage.Insert(ctx, newRow)
	if err != nil {
		return 0, fmt.Errorf("inserting row: %w", err)
	}

	b.byKeys.Set(newRow)
	b.broadcaster.Stream([]*server.Event{newRow})
	return newRevision, nil
}

func (b *backend) Update(ctx context.Context, key string, value []byte, revision, lease int64) (revRet int64, retKV *server.KeyValue, updateRet bool, errRet error) {
	defer func() {
		b.trace(ctx, "UPDATE", "key", key, "size", len(value), "rev", revision, "lease", lease, "ret", revRet, "retKV", retKV != nil, "updateRet", updateRet, "err", errRet)
	}()

	b.m.Lock()
	defer b.m.Unlock()

	rev, event, err := b.getLatest(key, revision, false)
	if err != nil {
		return rev, nil, false, err
	}
	if event == nil {
		return 0, nil, false, nil
	}
	if event.KV.ModRevision != revision {
		return rev, event.KV, false, nil
	}

	newRevision := b.nextRevision()
	newRow := &server.Event{
		KV: &server.KeyValue{
			Key:            key,
			CreateRevision: event.KV.CreateRevision,
			ModRevision:    newRevision,
			Value:          value,
			Lease:          lease,
		},
		PrevKV: event.KV,
	}

	err = b.storage.Insert(ctx, newRow)
	if err != nil {
		return newRevision, event.KV, false, fmt.Errorf("inserting row: %w", err)
	}

	b.byKeys.Set(newRow)
	b.broadcaster.Stream([]*server.Event{newRow})
	return newRevision, newRow.KV, true, err
}

func (b *backend) Delete(ctx context.Context, key string, revision int64) (revRet int64, retKV *server.KeyValue, deleteRet bool, errRet error) {
	defer func() {
		b.trace(ctx, "DELETE", "key", key, "rev", revision, "ret", revRet, "retKV", retKV != nil, "deleteRet", deleteRet, "err", errRet)
	}()

	b.m.Lock()
	defer b.m.Unlock()

	rev, prevEvent, err := b.getLatest(key, revision, false)
	if err != nil {
		return rev, nil, false, err
	}
	if prevEvent == nil {
		return rev, nil, true, nil
	}
	if prevEvent.Delete {
		return rev, prevEvent.KV, true, nil
	}
	if revision != 0 && prevEvent.KV.ModRevision != revision {
		return rev, prevEvent.KV, false, nil
	}

	newRevision := b.nextRevision()
	newRow := &server.Event{
		Delete: true,
		KV: &server.KeyValue{
			Key:            prevEvent.KV.Key,
			CreateRevision: prevEvent.KV.CreateRevision,
			ModRevision:    newRevision,
			Value:          prevEvent.KV.Value,
			Lease:          prevEvent.KV.Lease,
		},
		PrevKV: prevEvent.KV,
	}

	err = b.storage.Insert(ctx, newRow)
	if err != nil {
		return newRevision, prevEvent.KV, false, fmt.Errorf("inserting row: %w", err)
	}

	b.byKeys.Set(newRow)
	b.broadcaster.Stream([]*server.Event{newRow})
	return newRevision, newRow.KV, true, nil
}

func (b *backend) nextRevision() int64 {
	b.currentRevision++
	return b.currentRevision
}

func (b *backend) List(ctx context.Context, prefix, startKey string, limit, revision int64) (ret int64, retKV []*server.KeyValue, errRet error) {
	defer func() {
		b.trace(ctx, "LIST", "prefix", prefix, "startKey", startKey, "limit", limit, "rev", revision, "retKV", len(retKV), "err", errRet)
	}()

	b.m.RLock()
	defer b.m.RUnlock()

	rev, retRows, err := b.list(prefix, startKey, limit, revision, false)
	if err != nil {
		return rev, nil, err
	}

	retKeyValue := make([]*server.KeyValue, 0, len(retRows))
	for _, row := range retRows {
		retKeyValue = append(retKeyValue, row.KV)
	}

	return b.currentRevision, retKeyValue, nil
}

func (b *backend) Count(_ context.Context, prefix, startKey string, revision int64) (int64, int64, error) {
	b.m.RLock()
	defer b.m.RUnlock()

	rev, items, err := b.list(prefix, startKey, 0, revision, false)
	if err != nil {
		return rev, 0, err
	}

	return rev, int64(len(items)), nil
}

func (b *backend) Watch(ctx context.Context, prefix string, revision int64) server.WatchResult {
	b.trace(ctx, "WATCH", "prefix", prefix, "revision", revision)

	// starting watching right away so we don't miss anything
	ctx, cancel := context.WithCancel(ctx)
	readChan := b.broadcaster.Subscribe(ctx)

	// include the current revision in list
	if revision > 0 {
		revision--
	}

	result := make(chan []*server.Event, 100)
	wr := server.WatchResult{Events: result}
	b.m.RLock()
	rev, kvs, err := b.list(prefix, "", 0, revision, true)
	b.m.RUnlock()
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			logrus.Errorf("Failed to list %s for revision %d: %v", prefix, revision, err)
		}
		if errors.Is(err, server.ErrCompacted) {
			wr.CompactRevision = b.lastCompacted
			wr.CurrentRevision = rev
		}
		cancel()
	}

	checkPrefix := strings.HasSuffix(prefix, "/")
	go func() {
		lastRevision := revision
		if len(kvs) > 0 {
			lastRevision = rev
		}

		if len(kvs) > 0 {
			result <- kvs
		}

		// always ensure we fully read the channel
		for i := range readChan {
			events, ok := filterMatch(i, checkPrefix, prefix)
			if ok {
				result <- filter(events, lastRevision)
			}
		}
		close(result)
		cancel()
	}()

	return wr
}

func filterMatch(events interface{}, checkPrefix bool, prefix string) ([]*server.Event, bool) {
	eventList := events.([]*server.Event)
	filteredEventList := make([]*server.Event, 0, len(eventList))

	for _, event := range eventList {
		if (checkPrefix && strings.HasPrefix(event.KV.Key, prefix)) || event.KV.Key == prefix {
			filteredEventList = append(filteredEventList, event)
		}
	}

	return filteredEventList, len(filteredEventList) > 0
}

func filter(events []*server.Event, rev int64) []*server.Event {
	for len(events) > 0 && events[0].KV.ModRevision <= rev {
		events = events[1:]
	}

	return events
}

func (b *backend) DbSize(ctx context.Context) (int64, error) {
	b.m.RLock()
	defer b.m.RUnlock()

	return b.storage.DbSize(ctx), nil
}

func (b *backend) CurrentRevision(_ context.Context) (int64, error) {
	b.m.RLock()
	defer b.m.RUnlock()

	return b.currentRevision, nil
}

func (b *backend) Compact(_ context.Context, _ int64) (int64, error) {
	b.m.RLock()
	defer b.m.RUnlock()

	return b.currentRevision, nil
}

func (b *backend) getLatest(key string, revision int64, includeDeleted bool) (int64, *server.Event, error) {
	rev, items, err := b.list(key, "", 1, revision, includeDeleted)
	if err != nil {
		return rev, nil, err
	} else if len(items) == 0 {
		return rev, nil, nil
	}

	return rev, items[0], nil
}

func (b *backend) list(prefix, startKey string, limit, revision int64, includeDeleted bool) (int64, []*server.Event, error) {
	if revision > b.currentRevision {
		return b.currentRevision, nil, server.ErrFutureRev
	}
	if revision != 0 && revision <= b.lastCompacted {
		return b.currentRevision, nil, server.ErrCompacted
	}

	checkPrefix := strings.HasSuffix(prefix, "/")
	if checkPrefix || prefix == startKey {
		startKey = ""
	}

	started := false
	retRows := map[string]*server.Event{}
	b.byKeys.Ascend(newKeyRevisionIter(prefix, revision), func(item *server.Event) bool {
		if checkPrefix && !strings.HasPrefix(item.KV.Key, prefix) {
			return false
		}
		if !checkPrefix && item.KV.Key != prefix {
			return false
		}
		if revision != 0 && item.KV.ModRevision < revision {
			return true
		}

		// check if started
		if !started {
			if startKey == "" || item.KV.Key == startKey {
				started = true
			} else {
				return true
			}
		}

		if item.Delete && !includeDeleted {
			delete(retRows, item.KV.Key)
		} else {
			_, ok := retRows[item.KV.Key]
			if !ok && limit > 0 && len(retRows) >= int(limit) {
				return false
			}

			retRows[item.KV.Key] = item
		}

		return true
	})

	retRowsArr := make([]*server.Event, 0, len(retRows))
	for _, row := range retRows {
		retRowsArr = append(retRowsArr, row)
	}

	return b.currentRevision, retRowsArr, nil
}

func newKeyRevisionIter(key string, revision int64) *server.Event {
	return &server.Event{KV: &server.KeyValue{Key: key, ModRevision: revision}}
}

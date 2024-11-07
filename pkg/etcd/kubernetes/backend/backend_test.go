package backend

import (
	"context"
	"testing"
	"time"

	"github.com/loft-sh/vcluster/pkg/etcd/kubernetes/server"
	"gotest.tools/v3/assert"
)

func TestBackend(t *testing.T) {
	ctx := context.Background()
	testBackend := NewBackend(NewStorage(NewMemoryPageCache()))

	rev, err := testBackend.Create(ctx, "/test/test", []byte("test"), 0)
	assert.NilError(t, err)
	assert.Equal(t, rev, int64(1))

	_, count, err := testBackend.Count(ctx, "/test/", "", 0)
	assert.NilError(t, err)
	assert.Equal(t, count, int64(1))

	rev, _, _, err = testBackend.Delete(ctx, "/test/test", 2)
	assert.ErrorIs(t, err, server.ErrFutureRev)

	rev, _, deleted, err := testBackend.Delete(ctx, "/test/test", 1)
	assert.NilError(t, err)
	assert.Equal(t, deleted, true)

	_, count, err = testBackend.Count(ctx, "/test/", "", 0)
	assert.NilError(t, err)
	assert.Equal(t, count, int64(0))

	rev, err = testBackend.Create(ctx, "/test/test123", []byte("test"), 0)
	assert.NilError(t, err)
	assert.Equal(t, rev, int64(3))

	rev, err = testBackend.Create(ctx, "/test/test1234", []byte("test"), 0)
	assert.NilError(t, err)
	assert.Equal(t, rev, int64(4))

	rev, newRow, updated, err := testBackend.Update(ctx, "/test/test1234", []byte("test123"), 4, 0)
	assert.NilError(t, err)
	assert.Equal(t, rev, int64(5))
	assert.Equal(t, newRow.Key, "/test/test1234")
	assert.Equal(t, string(newRow.Value), "test123")
	assert.Equal(t, updated, true)

	_, count, err = testBackend.Count(ctx, "/test/", "", 0)
	assert.NilError(t, err)
	assert.Equal(t, count, int64(2))

	_, count, err = testBackend.Count(ctx, "/test/", "", 4)
	assert.NilError(t, err)
	assert.Equal(t, count, int64(1))

	_, count, err = testBackend.Count(ctx, "/test/", "", 5)
	assert.NilError(t, err)
	assert.Equal(t, count, int64(1))
}

func TestBackendGet(t *testing.T) {
	ctx := context.Background()
	testBackend := NewBackend(NewStorage(NewMemoryPageCache()))

	rev, err := testBackend.Create(ctx, "/registry/clusterrolebindings/system:node", []byte("test"), 0)
	assert.NilError(t, err)
	assert.Equal(t, rev, int64(1))

	rev, kv, err := testBackend.Get(ctx, "/registry/clusterrolebindings/system:node", 0)
	assert.NilError(t, err)
	assert.Equal(t, rev, int64(1))
	assert.Equal(t, kv.Lease, int64(0))
	assert.Equal(t, string(kv.Value), "test")
	assert.Equal(t, kv.CreateRevision, int64(1))

	rev, kvs, err := testBackend.List(ctx, "/registry/clusterrolebindings/", "/registry/clusterrolebindings/", 0, 0)
	assert.NilError(t, err)
	assert.Equal(t, rev, int64(1))
	assert.Equal(t, len(kvs), 1)
	assert.Equal(t, kvs[0].Key, "/registry/clusterrolebindings/system:node")
	assert.Equal(t, string(kvs[0].Value), "test")

	rev, kv, deleted, err := testBackend.Delete(ctx, "/registry/clusterrolebindings/system:node", 1)
	assert.NilError(t, err)
	assert.Equal(t, rev, int64(2))
	assert.Equal(t, string(kv.Value), "test")
	assert.Equal(t, deleted, true)

	rev, err = testBackend.Create(ctx, "/registry/clusterrolebindings/system:node", []byte("test"), 0)
	assert.NilError(t, err)
	assert.Equal(t, rev, int64(3))
}

func TestBackendWatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	testBackend := NewBackend(NewStorage(NewMemoryPageCache()))

	watchResult := testBackend.Watch(ctx, "/test/", 0)

	rev, err := testBackend.Create(ctx, "/test/test1234", []byte("test"), 0)
	assert.NilError(t, err)
	assert.Equal(t, rev, int64(1))

	events := getEvents(ctx, watchResult)
	assert.Equal(t, len(events), 1)
	assert.Equal(t, events[0].KV.CreateRevision, int64(1))
	assert.Equal(t, events[0].KV.Key, "/test/test1234")
	assert.Equal(t, string(events[0].KV.Value), "test")
	assert.Equal(t, events[0].Create, true)
}

func getEvents(ctx context.Context, watchResult server.WatchResult) []*server.Event {
	select {
	case <-ctx.Done():
		return nil
	case event := <-watchResult.Events:
		return event
	}
}

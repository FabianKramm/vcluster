package server

import (
	"context"

	"go.etcd.io/etcd/api/v3/etcdserverpb"
)

func (l *LimitedServer) get(ctx context.Context, r *etcdserverpb.RangeRequest) (*RangeResponse, error) {
	rev, kv, err := l.backend.Get(ctx, string(r.Key), r.Revision)
	resp := &RangeResponse{
		Header: txnHeader(rev),
	}
	if kv != nil {
		resp.Kvs = []*KeyValue{kv}
		resp.Count = 1
	}
	return resp, err
}

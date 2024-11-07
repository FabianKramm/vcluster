package backend

import (
	"time"

	"github.com/gabstv/go-bsdiff/pkg/bsdiff"
	"github.com/gabstv/go-bsdiff/pkg/bspatch"
	"github.com/loft-sh/vcluster/pkg/etcd/kubernetes/server"
)

type Event struct {
	Expires int64 // unix timestamp when this event will expire

	Diff bool

	Server *server.Event
}

func (e *Event) ToServerEvent() (*server.Event, error) {
	var retValue []byte
	if e.Diff {
		var err error
		retValue, err = bspatch.Bytes(e.Server.KV.Value, e.Server.PrevKV.Value)
		if err != nil {
			return nil, err
		}
	} else if e.Server.PrevKV != nil {
		retValue = e.Server.PrevKV.Value
	}

	return &server.Event{
		Delete: e.Server.Delete,
		Create: e.Server.Create,
		KV:     e.Server.KV,
		PrevKV: &server.KeyValue{
			Key:            e.Server.PrevKV.Key,
			CreateRevision: e.Server.PrevKV.CreateRevision,
			ModRevision:    e.Server.PrevKV.ModRevision,
			Value:          retValue,
			Lease:          e.Server.PrevKV.Lease,
		},
	}, nil
}

func fromServerEvent(event *server.Event) *Event {
	diff := false
	if event.KV != nil && event.PrevKV != nil && len(event.KV.Value) > 500 && len(event.PrevKV.Value) > 0 {
		patch, err := bsdiff.Bytes(event.KV.Value, event.PrevKV.Value)
		if err == nil {
			event = &server.Event{
				Delete: event.Delete,
				Create: event.Create,
				KV:     event.KV,
				PrevKV: &server.KeyValue{
					Key:            event.PrevKV.Key,
					CreateRevision: event.PrevKV.CreateRevision,
					ModRevision:    event.PrevKV.ModRevision,
					Value:          patch,
					Lease:          event.PrevKV.Lease,
				},
			}
			diff = true
		}
	}

	expiresAt := int64(0)
	if event.KV.Lease > 0 {
		expiresAt = time.Now().Add(time.Second * time.Duration(event.KV.Lease)).Unix()
	}

	return &Event{
		Diff:    diff,
		Expires: expiresAt,
		Server:  event,
	}
}

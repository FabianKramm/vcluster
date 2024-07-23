package store

import (
	"context"
	"testing"

	"github.com/loft-sh/vcluster/pkg/syncer/synccontext"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestStore(t *testing.T) {
	store, ok := NewStore(NewMemoryBackend()).(*Store)
	assert.Equal(t, true, ok)

	gvk := corev1.SchemeGroupVersion.WithKind("Secret")
	virtualName := types.NamespacedName{
		Name:      "virtual-name",
		Namespace: "virtual-namespace",
	}
	hostName := types.NamespacedName{
		Name:      "host-name",
		Namespace: "host-namespace",
	}

	baseCtx := context.TODO()
	baseCtx = synccontext.WithMapping(baseCtx, synccontext.NameMapping{
		GroupVersionKind: gvk,
		VirtualName:      virtualName,
	})

	// record reference
	err := store.RecordReference(baseCtx, synccontext.NameMapping{
		GroupVersionKind: gvk,
		HostName:         hostName,
		VirtualName:      virtualName,
	})
	assert.NilError(t, err)

	// virtual -> host
	translatedHostName, ok := store.VirtualToHostName(baseCtx, synccontext.Object{
		GroupVersionKind: gvk,
		NamespacedName:   virtualName,
	})
	assert.Equal(t, true, ok)
	assert.Equal(t, hostName, translatedHostName)

	// virtual -> host
	translatedVirtualName, ok := store.HostToVirtualName(baseCtx, synccontext.Object{
		GroupVersionKind: gvk,
		NamespacedName:   hostName,
	})
	assert.Equal(t, true, ok)
	assert.Equal(t, virtualName, translatedVirtualName)

	// virtual -> host
	_, ok = store.HostToVirtualName(baseCtx, synccontext.Object{
		GroupVersionKind: gvk,
	})
	assert.Equal(t, false, ok)

	// check inner structure of store
	assert.Equal(t, 1, len(store.mappings))
	assert.Equal(t, 0, len(store.hostToVirtualLabel))
	assert.Equal(t, 0, len(store.hostToVirtualLabelCluster))
	assert.Equal(t, 0, len(store.virtualToHostLabel))
	assert.Equal(t, 0, len(store.virtualToHostLabelCluster))
	assert.Equal(t, 1, len(store.hostToVirtualName))
	assert.Equal(t, 1, len(store.virtualToHostName))

	// make sure the mapping is not added
	nameMapping := synccontext.NameMapping{
		GroupVersionKind: gvk,
		HostName:         hostName,
		VirtualName:      virtualName,
	}
	err = store.RecordReference(baseCtx, nameMapping)
	assert.NilError(t, err)
	assert.Equal(t, 1, len(store.mappings))
	assert.Equal(t, 0, len(store.hostToVirtualLabel))
	assert.Equal(t, 0, len(store.hostToVirtualLabelCluster))
	assert.Equal(t, 0, len(store.virtualToHostLabel))
	assert.Equal(t, 0, len(store.virtualToHostLabelCluster))
	assert.Equal(t, 1, len(store.hostToVirtualName))
	assert.Equal(t, 1, len(store.virtualToHostName))

	// validate mapping itself
	mapping, ok := store.mappings[nameMapping]
	assert.Equal(t, true, ok)
	assert.Equal(t, 0, len(mapping.References))
	assert.Equal(t, 0, len(mapping.Labels))
	assert.Equal(t, 0, len(mapping.LabelsCluster))
}

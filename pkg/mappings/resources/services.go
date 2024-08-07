package resources

import (
	"github.com/loft-sh/vcluster/pkg/mappings/generic"
	"github.com/loft-sh/vcluster/pkg/syncer/synccontext"
	"github.com/loft-sh/vcluster/pkg/util/translate"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func CreateServiceMapper(ctx *synccontext.RegisterContext) (synccontext.Mapper, error) {
	mapper, err := generic.NewMapper(ctx, &corev1.Service{}, translate.Default.HostName)
	if err != nil {
		return nil, err
	}

	return &servicesMapper{
		Mapper: mapper,
	}, nil
}

type servicesMapper struct {
	synccontext.Mapper
}

func (s *servicesMapper) VirtualToHost(ctx *synccontext.SyncContext, req types.NamespacedName, vObj client.Object) types.NamespacedName {
	if req.Name == "kubernetes" && req.Namespace == "default" {
		return types.NamespacedName{
			Name:      translate.VClusterName,
			Namespace: ctx.CurrentNamespace,
		}
	}

	return s.Mapper.VirtualToHost(ctx, req, vObj)
}

func (s *servicesMapper) HostToVirtual(ctx *synccontext.SyncContext, req types.NamespacedName, pObj client.Object) types.NamespacedName {
	if req.Name == translate.VClusterName && req.Namespace == ctx.CurrentNamespace {
		return types.NamespacedName{
			Name:      "kubernetes",
			Namespace: "default",
		}
	}

	return s.Mapper.HostToVirtual(ctx, req, pObj)
}

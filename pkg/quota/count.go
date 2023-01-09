package quota

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

func ObjectUsageFunc(obj runtime.Object, restMapper meta.RESTMapper, scheme *runtime.Scheme) (corev1.ResourceList, error) {
	result := corev1.ResourceList{}
	gvk, err := apiutil.GVKForObject(obj, scheme)
	if err != nil {
		return nil, err
	}

	mapping, err := restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, err
	}

	result[ObjectCountQuotaResourceNameFor(mapping.Resource.GroupResource())] = resource.MustParse("1")
	return result, nil
}

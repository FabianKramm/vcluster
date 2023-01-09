package quota

import (
	"fmt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
)

// the name used for object count quota
var pvcObjectCountName = ObjectCountQuotaResourceNameFor(corev1.SchemeGroupVersion.WithResource("persistentvolumeclaims").GroupResource())

// storageClassSuffix is the suffix to the qualified portion of storage class resource name.
// For example, if you want to quota storage by storage class, you would have a declaration
// that follows <storage-class>.storageclass.storage.k8s.io/<resource>.
// For example:
// * gold.storageclass.storage.k8s.io/: 500Gi
// * bronze.storageclass.storage.k8s.io/requests.storage: 500Gi
const storageClassSuffix string = ".storageclass.storage.k8s.io/"

// PersistentVolumeClaimUsageFunc knows how to measure usage associated with item.
func PersistentVolumeClaimUsageFunc(item runtime.Object) (corev1.ResourceList, error) {
	result := corev1.ResourceList{}
	pvc, err := toExternalPersistentVolumeClaimOrError(item)
	if err != nil {
		return result, err
	}

	// charge for claim
	result[corev1.ResourcePersistentVolumeClaims] = *(resource.NewQuantity(1, resource.DecimalSI))
	result[pvcObjectCountName] = *(resource.NewQuantity(1, resource.DecimalSI))
	storageClassRef := GetPersistentVolumeClaimClass(pvc)
	if len(storageClassRef) > 0 {
		storageClassClaim := corev1.ResourceName(storageClassRef + storageClassSuffix + string(corev1.ResourcePersistentVolumeClaims))
		result[storageClassClaim] = *(resource.NewQuantity(1, resource.DecimalSI))
	}

	// charge for storage
	if request, found := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; found {
		result[corev1.ResourceRequestsStorage] = request
		// charge usage to the storage class (if present)
		if len(storageClassRef) > 0 {
			storageClassStorage := corev1.ResourceName(storageClassRef + storageClassSuffix + string(corev1.ResourceRequestsStorage))
			result[storageClassStorage] = request
		}
	}
	return result, nil
}

// GetPersistentVolumeClaimClass returns StorageClassName. If no storage class was
// requested, it returns "".
func GetPersistentVolumeClaimClass(claim *corev1.PersistentVolumeClaim) string {
	// Use beta annotation first
	if class, found := claim.Annotations[corev1.BetaStorageClassAnnotation]; found {
		return class
	}

	if claim.Spec.StorageClassName != nil {
		return *claim.Spec.StorageClassName
	}

	return ""
}

func toExternalPersistentVolumeClaimOrError(obj runtime.Object) (*corev1.PersistentVolumeClaim, error) {
	pvc := &corev1.PersistentVolumeClaim{}
	switch t := obj.(type) {
	case *corev1.PersistentVolumeClaim:
		pvc = t
	default:
		return nil, fmt.Errorf("expect *api.PersistentVolumeClaim or *v1.PersistentVolumeClaim, got %v", t)
	}
	return pvc, nil
}

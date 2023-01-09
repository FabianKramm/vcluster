package quota

import corev1 "k8s.io/api/core/v1"

type evaluator struct {
	used  corev1.ResourceList
	quota corev1.ResourceList
}

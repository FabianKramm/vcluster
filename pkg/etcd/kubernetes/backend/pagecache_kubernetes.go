package backend

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type PageCache interface {
	Create(ctx context.Context) (Page, error)
	Delete(ctx context.Context, page Page) error
	List(ctx context.Context) ([]Page, error)
}

func NewKubernetesPageCache(client kubernetes.Interface, labelSelector string, namespace string) PageCache {
	parsedLabelSelector, err := metav1.ParseToLabelSelector(labelSelector)
	if err != nil {
		panic(err)
	}

	return &kubernetesPageCache{
		client: client,

		parsedLabelSelector: parsedLabelSelector.MatchLabels,

		labelSelector: labelSelector,
		namespace:     namespace,
	}
}

type kubernetesPageCache struct {
	client kubernetes.Interface

	parsedLabelSelector map[string]string
	labelSelector       string
	namespace           string
}

func (p *kubernetesPageCache) Create(ctx context.Context) (Page, error) {
	secret, err := p.client.CoreV1().Secrets(p.namespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "vcluster-secret-page-",
			Labels:       p.parsedLabelSelector,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	return newKubernetesPage(p.client, secret), nil
}

func (p *kubernetesPageCache) Delete(ctx context.Context, page Page) error {
	secret := page.(*kubernetesPage).secret
	return p.client.CoreV1().Secrets(secret.Namespace).Delete(ctx, secret.Name, metav1.DeleteOptions{})
}

func (p *kubernetesPageCache) List(ctx context.Context) ([]Page, error) {
	secrets, err := p.client.CoreV1().Secrets(p.namespace).List(ctx, metav1.ListOptions{LabelSelector: p.labelSelector})
	if err != nil {
		return nil, err
	}

	retPages := make([]Page, 0, len(secrets.Items))
	for _, secret := range secrets.Items {
		retPages = append(retPages, newKubernetesPage(p.client, &secret))
	}

	return retPages, nil
}

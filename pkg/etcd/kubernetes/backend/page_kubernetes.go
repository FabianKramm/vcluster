package backend

import (
	"context"
	"encoding/base64"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const secretDataKey = "payload"

type Page interface {
	Size() int
	SizeWithRow(row *Event) (int, error)

	Rows() ([]*Event, error)

	Insert(ctx context.Context, row *Event) error
	Delete(ctx context.Context, revision ...int64) error
}

func newKubernetesPage(client kubernetes.Interface, secret *corev1.Secret) Page {
	size := 0
	if secret.Data[secretDataKey] != nil {
		size = len(base64.StdEncoding.EncodeToString(secret.Data[secretDataKey]))
	}

	return &kubernetesPage{
		client: client,
		secret: secret,
		size:   size,
	}
}

type kubernetesPage struct {
	client kubernetes.Interface

	secret *corev1.Secret

	size int
}

func (p *kubernetesPage) Rows() ([]*Event, error) {
	rowMap, err := p.decode()
	if err != nil {
		return nil, err
	}

	retArr := make([]*Event, 0, len(rowMap))
	for _, row := range rowMap {
		retArr = append(retArr, row)
	}

	return retArr, nil
}

func (p *kubernetesPage) Delete(ctx context.Context, revisions ...int64) error {
	rowMap, err := p.decode()
	if err != nil {
		return err
	}

	// doesn't exist?
	beforeLength := len(rowMap)
	for _, revision := range revisions {
		delete(rowMap, revision)
	}
	if len(rowMap) == beforeLength {
		return nil
	}

	// if empty delete the complete data
	newSecret := p.secret.DeepCopy()
	newSize := 0
	if len(rowMap) == 0 {
		newSecret.Data = nil
	} else {
		encoded, err := encode(rowMap)
		if err != nil {
			return err
		}

		newSecret.Data[secretDataKey] = encoded
		newSize = len(base64.StdEncoding.EncodeToString(encoded))
	}
	newSecret, err = p.client.CoreV1().Secrets(p.secret.Namespace).Update(ctx, newSecret, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	p.secret = newSecret
	p.size = newSize
	return nil
}

func (p *kubernetesPage) Insert(ctx context.Context, row *Event) error {
	rowMap, err := p.decode()
	if err != nil {
		return err
	}

	rowMap[row.Server.KV.ModRevision] = row
	encoded, err := encode(rowMap)
	if err != nil {
		return err
	}

	newSecret := p.secret.DeepCopy()
	if newSecret.Data == nil {
		newSecret.Data = map[string][]byte{}
	}
	newSecret.Data[secretDataKey] = encoded
	newSecret, err = p.client.CoreV1().Secrets(p.secret.Namespace).Update(ctx, newSecret, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	p.secret = newSecret
	p.size = len(base64.StdEncoding.EncodeToString(encoded))
	return nil
}

func (p *kubernetesPage) SizeWithRow(row *Event) (int, error) {
	rowMap, err := p.decode()
	if err != nil {
		return 0, err
	}

	rowMap[row.Server.KV.ModRevision] = row
	encoded, err := encode(rowMap)
	if err != nil {
		return 0, err
	}

	return len(base64.StdEncoding.EncodeToString(encoded)), nil
}

func (p *kubernetesPage) Size() int {
	return p.size
}

func (p *kubernetesPage) decode() (map[int64]*Event, error) {
	if p.secret.Data == nil {
		return make(map[int64]*Event), nil
	}

	return decode(p.secret.Data[secretDataKey])
}

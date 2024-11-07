package backend

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/gob"
	"io"

	"github.com/loft-sh/vcluster/pkg/etcd/kubernetes/server"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const secretDataKey = "payload"

type Page interface {
	Size() int
	SizeWithRow(row *server.Event) (int, error)

	Rows() ([]*server.Event, error)

	Insert(ctx context.Context, row *server.Event) error
	Delete(ctx context.Context, row *server.Event) error
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

func (p *kubernetesPage) Rows() ([]*server.Event, error) {
	rowMap, err := p.decode()
	if err != nil {
		return nil, err
	}

	retArr := make([]*server.Event, 0, len(rowMap))
	for _, row := range rowMap {
		retArr = append(retArr, row)
	}

	return retArr, nil
}

func (p *kubernetesPage) Delete(ctx context.Context, row *server.Event) error {
	rowMap, err := p.decode()
	if err != nil {
		return err
	}

	// doesn't exist?
	if rowMap[row.KV.ModRevision] == nil {
		return nil
	}

	delete(rowMap, row.KV.ModRevision)

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

func (p *kubernetesPage) Insert(ctx context.Context, row *server.Event) error {
	rowMap, err := p.decode()
	if err != nil {
		return err
	}

	rowMap[row.KV.ModRevision] = row
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

func (p *kubernetesPage) SizeWithRow(row *server.Event) (int, error) {
	rowMap, err := p.decode()
	if err != nil {
		return 0, err
	}

	rowMap[row.KV.ModRevision] = row
	encoded, err := encode(rowMap)
	if err != nil {
		return 0, err
	}

	return len(base64.StdEncoding.EncodeToString(encoded)), nil
}

func (p *kubernetesPage) Size() int {
	return p.size
}

func (p *kubernetesPage) decode() (map[int64]*server.Event, error) {
	if p.secret.Data == nil {
		return make(map[int64]*server.Event), nil
	}

	return decode(p.secret.Data[secretDataKey])
}

func decode(compressedPayload []byte) (map[int64]*server.Event, error) {
	payload, err := decompress(compressedPayload)
	if err != nil {
		return nil, err
	}

	retMap := map[int64]*server.Event{}
	err = gob.NewDecoder(bytes.NewReader(payload)).Decode(&retMap)
	if err != nil {
		return nil, err
	}

	return retMap, nil
}

func encode(m map[int64]*server.Event) ([]byte, error) {
	var buf bytes.Buffer
	err := gob.NewEncoder(&buf).Encode(m)
	if err != nil {
		return nil, err
	}

	return compress(buf.Bytes())
}

func decompress(payload []byte) ([]byte, error) {
	rdata := bytes.NewReader(payload)
	r, err := gzip.NewReader(rdata)
	if err != nil {
		return nil, err
	}

	decompressed, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	return decompressed, nil
}

func compress(s []byte) ([]byte, error) {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)

	_, err := gz.Write(s)
	if err != nil {
		return nil, err
	}

	err = gz.Flush()
	if err != nil {
		return nil, err
	}

	err = gz.Close()
	if err != nil {
		return nil, err
	}

	return b.Bytes(), nil
}

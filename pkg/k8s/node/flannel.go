package node

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/loft-sh/vcluster/pkg/k8s/command"
	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

const (
	cniConf = `{
  "name":"cbr0",
  "cniVersion":"1.0.0",
  "plugins":[
    {
      "type":"flannel",
      "delegate":{
        "hairpinMode":true,
        "forceAddress":true,
        "isDefaultGateway":true
      }
    },
    {
      "type":"portmap",
      "capabilities":{
        "portMappings":true
      }
    },
    {
      "type":"bandwidth",
      "capabilities":{
        "bandwidth":true
      }
    }
  ]
}
`
)

const (
	flannelCNIConf = "/etc/cni/net.d/10-flannel.conflist"
	flannelConfig  = "/data/etc/flannel/net-conf.json"
)

func SetupFlannel(clusterCIDR string) error {
	err := createCNIConf()
	if err != nil {
		return err
	}

	err = writeFlannelConf(clusterCIDR)
	if err != nil {
		return err
	}

	return nil
}

func StartFlannel(ctx context.Context, eg *errgroup.Group, nodeName string) error {
	// start flannel
	eg.Go(func() error {
		// wait for pod to get ready
		apiURL, err := waitForPodCIDR(ctx, nodeName)
		if err != nil {
			klog.FromContext(ctx).Error(err, "wait for pod cidr")
			return err
		}

		// build flags
		args := []string{}
		args = append(args, "/usr/local/bin/flanneld")
		args = append(args, "--kube-api-url="+apiURL)
		args = append(args, "--kubeconfig-file="+KubeletKubeConfig)
		args = append(args, "--net-config-path="+flannelConfig)
		args = append(args, "--ip-masq")
		args = append(args, "--kube-subnet-mgr")

		// start flanneld
		err = command.RunCommand(ctx, args, "flanneld", "NODE_NAME="+nodeName)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
			return err
		}

		return nil
	})

	return nil
}

// waitForPodCIDR watches nodes with this node's name, and returns when the PodCIDR has been set.
func waitForPodCIDR(ctx context.Context, nodeName string) (string, error) {
	kubeConfig, err := clientcmd.LoadFromFile(KubeletKubeConfig)
	if err != nil {
		return "", err
	}
	kubeRestConfig, err := clientcmd.NewDefaultClientConfig(*kubeConfig, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return "", err
	}
	kubeClient, err := kubernetes.NewForConfig(kubeRestConfig)
	if err != nil {
		return "", err
	}

	err = wait.PollUntilContextCancel(ctx, time.Second*2, true, func(ctx context.Context) (done bool, err error) {
		node, err := kubeClient.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
		if err != nil {
			klog.V(1).Info("Couldn't get node for flannel", "error", err)
			return false, nil
		} else if node.Spec.PodCIDR != "" {
			return true, nil
		}

		klog.V(1).Info("Waiting for node to receive pod cidr")
		return false, nil
	})
	if err != nil {
		return "", fmt.Errorf("wait for pod cidr: %w", err)
	}

	klog.Info("Flannel found PodCIDR assigned for node " + nodeName)
	return kubeRestConfig.Host, nil
}

func writeFlannelConf(clusterCIDR string) error {
	configBytes, err := json.Marshal(map[string]any{
		"Network":     clusterCIDR,
		"EnableIPv6":  false,
		"EnableIPv4":  true,
		"IPv6Network": "::/0",
		"Backend": map[string]string{
			"Type": "vxlan",
		},
	})
	if err != nil {
		return err
	}

	err = os.MkdirAll(filepath.Dir(flannelConfig), 0755)
	if err != nil {
		return err
	}

	return os.WriteFile(flannelConfig, configBytes, 0666)
}

func createCNIConf() error {
	err := os.MkdirAll(filepath.Dir(flannelCNIConf), 0755)
	if err != nil {
		return err
	}

	return os.WriteFile(flannelCNIConf, []byte(cniConf), 0666)
}

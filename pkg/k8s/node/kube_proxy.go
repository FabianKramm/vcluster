package node

import (
	"context"
	"fmt"
	"os"

	"github.com/loft-sh/vcluster/pkg/k8s/command"
	"golang.org/x/sync/errgroup"
)

const (
	kubeProxyKubeConfig = "/data/pki/kube-proxy.conf"
)

func StartKubeProxy(ctx context.Context, eg *errgroup.Group, nodeName, clusterCIDR string) error {
	// start kubelet command
	eg.Go(func() error {
		// build flags
		args := []string{}
		args = append(args, "/usr/local/bin/kube-proxy")
		args = append(args, "--proxy-mode=iptables")
		args = append(args, "--kubeconfig="+kubeProxyKubeConfig)
		args = append(args, "--cluster-cidr="+clusterCIDR)
		args = append(args, "--conntrack-max-per-core=0")
		args = append(args, "--conntrack-tcp-timeout-established=0s")
		args = append(args, "--conntrack-tcp-timeout-close-wait=0s")
		args = append(args, "--hostname-override="+nodeName)

		// start kube-proxy
		err := command.RunCommand(ctx, args, "kube-proxy")
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
			return err
		}

		return nil
	})

	return nil
}

package node

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"
	nodeutil "k8s.io/component-helpers/node/util"
)

func StartNode(ctx context.Context, caCertPath, clusterCIDR, clusterDomain string) error {
	// get node name
	nodeName, err := nodeutil.GetHostname("")
	if err != nil {
		return fmt.Errorf("failed to determine node name: %w", err)
	}

	// do kernel setup
	KernelSetup()

	// evacuate cgroup
	err = EvacuateCgroup2()
	if err != nil {
		return fmt.Errorf("evacuate cgroup: %w", err)
	}

	// start components
	eg := &errgroup.Group{}

	// setup flannel
	err = SetupFlannel(clusterCIDR)
	if err != nil {
		return fmt.Errorf("setup flannel: %w", err)
	}

	// start containerd
	err = StartContainerd(ctx, eg)
	if err != nil {
		return fmt.Errorf("start containerd: %w", err)
	}

	// start kubelet
	err = StartKubelet(ctx, eg, caCertPath, nodeName, clusterCIDR, clusterDomain)
	if err != nil {
		return fmt.Errorf("start kubelet: %w", err)
	}

	// start kube-proxy
	err = StartKubeProxy(ctx, eg, nodeName, clusterCIDR)
	if err != nil {
		return fmt.Errorf("start kube-proxy: %w", err)
	}

	// start flannel
	err = StartFlannel(ctx, eg, nodeName)
	if err != nil {
		return fmt.Errorf("start flannel: %w", err)
	}

	// regular stop case, will return as soon as a component returns an error.
	// we don't expect the components to stop by themselves since they're supposed
	// to run until killed or until they fail
	err = eg.Wait()
	if err == nil || err.Error() == "signal: killed" {
		return nil
	}
	return err
}

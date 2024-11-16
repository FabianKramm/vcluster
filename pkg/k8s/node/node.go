package node

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"
	nodeutil "k8s.io/component-helpers/node/util"
)

func StartNode(ctx context.Context, caCertPath, bootstrapKubeConfig string) error {
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

	// start containerd
	err = StartContainerd(ctx, eg)
	if err != nil {
		return fmt.Errorf("start containerd: %w", err)
	}

	// start kubelet
	err = StartKubelet(ctx, eg, caCertPath, bootstrapKubeConfig, nodeName)
	if err != nil {
		return fmt.Errorf("start kubelet: %w", err)
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

package node

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path"
	"time"

	"github.com/loft-sh/vcluster/pkg/k8s/command"
	containerruntime "github.com/loft-sh/vcluster/pkg/k8s/node/runtime"
	"github.com/pelletier/go-toml"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	criconfig "github.com/containerd/containerd/pkg/cri/config"
)

const (
	containerdRoot      = "/data/containerd/root"
	containerdState     = "/data/containerd/state"
	containerdSock      = "/data/containerd/containerd.sock"
	containerdConfig    = "/data/containerd/containerd.toml"
	containerdCriConfig = "/data/containerd/containerd-cri.toml"

	KubePauseContainerImage        = "registry.k8s.io/pause"
	KubePauseContainerImageVersion = "3.9"
)

func StartContainerd(ctx context.Context, eg *errgroup.Group) error {
	// create containerd config
	err := createContainerdConfig()
	if err != nil {
		return fmt.Errorf("create containerd config: %w", err)
	}

	// start containerd command
	eg.Go(func() error {
		err := os.MkdirAll("/data/containerd/root", 0755)
		if err != nil {
			return fmt.Errorf("make root containerd dir: %w", err)
		}
		err = os.MkdirAll("/data/containerd/state", 0755)
		if err != nil {
			return fmt.Errorf("make root containerd dir: %w", err)
		}

		// build flags
		args := []string{}
		args = append(args, "/usr/local/bin/containerd")
		args = append(args, "--root="+containerdRoot)
		args = append(args, "--state="+containerdState)
		args = append(args, "--address="+containerdSock)
		args = append(args, "--config="+containerdConfig)

		err = command.RunCommand(ctx, args, "containerd")
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
			return err
		}

		return nil
	})

	return wait.ExponentialBackoffWithContext(ctx, wait.Backoff{
		Duration: 100 * time.Millisecond, Factor: 1.2, Jitter: 0.05, Steps: 30,
	}, func(ctx context.Context) (bool, error) {
		rt := containerruntime.NewContainerRuntime(&url.URL{Scheme: "unix", Path: containerdSock})
		if err := rt.Ping(ctx); err != nil {
			klog.V(1).Info("Failed to ping containerd", "error", err)
			return false, nil
		}

		klog.V(1).Info("Successfully pinged containerd")
		return true, nil
	})
}

func createContainerdConfig() error {
	criConfig, err := generateDefaultCRIConfig(KubePauseContainerImage + ":" + KubePauseContainerImageVersion)
	if err != nil {
		return fmt.Errorf("generate default cri config: %w", err)
	}

	err = os.MkdirAll(path.Dir(containerdCriConfig), 0755)
	if err != nil {
		return fmt.Errorf("make root containerd dir: %w", err)
	}

	err = os.WriteFile(containerdCriConfig, criConfig, 0666)
	if err != nil {
		return fmt.Errorf("write cri config: %w", err)
	}

	err = os.WriteFile(containerdConfig, []byte(`# vcluster_managed=true
# This is a placeholder configuration for vCluster managed containerd.
# If you wish to override the config, remove the first line and replace this file with your custom configuration.
# For reference see https://github.com/containerd/containerd/blob/main/docs/man/containerd-config.toml.5.md
version = 2
imports = ["`+containerdCriConfig+`"]
`), 0666)
	if err != nil {
		return fmt.Errorf("write containerd config: %w", err)
	}

	return nil
}

// Returns the default containerd config, including only the CRI plugin
// configuration, using the given image for sandbox containers. Uses the
// containerd package to generate all the rest, so this will be in sync with
// containerd's defaults for the CRI plugin.
func generateDefaultCRIConfig(sandboxContainerImage string) ([]byte, error) {
	criPluginConfig := criconfig.DefaultConfig()
	// Set pause image
	criPluginConfig.SandboxImage = sandboxContainerImage

	return toml.Marshal(map[string]any{
		"version": 2,
		"plugins": map[string]any{
			"io.containerd.grpc.v1.cri": criPluginConfig,
		},
	})
}

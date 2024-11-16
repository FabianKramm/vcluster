package node

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"runtime"

	"github.com/ghodss/yaml"
	"github.com/loft-sh/vcluster/pkg/k8s/command"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/certificate/bootstrap"
	"k8s.io/utils/ptr"

	kubeletv1beta1 "k8s.io/kubelet/config/v1beta1"
)

const (
	kubeletDataDir    = "/data/kubelet/root"
	kubeletCertDir    = "/data/kubelet/pki"
	kubeletKubeConfig = "/data/kubelet/pki/kube-config.yaml"
	kubeletConfig     = "/data/kubelet/kubelet-config.yaml"
)

func StartKubelet(ctx context.Context, eg *errgroup.Group, caCertPath, kubeConfigPath, nodeName string) error {
	// write kubelet certs
	err := writeKubeletCerts(ctx, kubeConfigPath, nodeName)
	if err != nil {
		return err
	}

	// write kubelet config
	err = writeKubeletConfig(kubeletConfig, caCertPath)
	if err != nil {
		return fmt.Errorf("write kubelet config: %w", err)
	}

	// start kubelet command
	eg.Go(func() error {
		err := os.MkdirAll(kubeletDataDir, 0755)
		if err != nil {
			return fmt.Errorf("make kubelet dir: %w", err)
		}

		// build flags
		args := []string{}
		args = append(args, "/usr/local/bin/kubelet")
		args = append(args, "--root-dir="+kubeletDataDir)
		args = append(args, "--cert-dir="+kubeletCertDir)
		args = append(args, "--config="+kubeletConfig)
		args = append(args, "--kubeconfig="+kubeletKubeConfig)
		args = append(args, "--containerd="+containerdSock)
		args = append(args, "--hostname-override="+nodeName)

		// figure out cgroups
		kubeletRoot, runtimeRoot, controllers := CheckCgroups()
		if !controllers["cpu"] {
			klog.FromContext(ctx).Info("Disabling CPU quotas due to missing cpu controller or cpu.cfs_period_us")
			args = append(args, "--cpu-cfs-quota=false")
		}
		if !controllers["pids"] {
			klog.Fatal("pids cgroup controller not found")
		}
		if kubeletRoot != "" {
			args = append(args, "--kubelet-cgroups="+kubeletRoot)
		}
		if runtimeRoot != "" {
			args = append(args, "--runtime-cgroups="+runtimeRoot)
		}

		err = command.RunCommand(ctx, args, "kubelet")
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
			return err
		}

		return nil
	})

	return nil
}

func writeKubeletCerts(ctx context.Context, kubeConfigPath, nodeName string) error {
	_, err := os.Stat(kubeletKubeConfig)
	if err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	err = os.MkdirAll(kubeletCertDir, 0755)
	if err != nil {
		return fmt.Errorf("make kubelet cert dir: %w", err)
	}

	err = bootstrap.LoadClientCert(
		ctx,
		kubeletKubeConfig,
		kubeConfigPath,
		kubeletCertDir,
		types.NodeName(nodeName),
	)
	if err != nil {
		return fmt.Errorf("create kubelet certificates: %w", err)
	}

	return nil
}

func writeKubeletConfig(path, caCertPath string) error {
	config := &kubeletv1beta1.KubeletConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: kubeletv1beta1.SchemeGroupVersion.String(),
			Kind:       "KubeletConfiguration",
		},
	}
	config.Authentication.X509.ClientCAFile = caCertPath
	config.ResolverConfig = determineKubeletResolvConfPath()
	config.ContainerRuntimeEndpoint = (&url.URL{Scheme: "unix", Path: containerdSock}).String()
	config.FailSwapOn = ptr.To(false)
	config.CgroupDriver = "cgroupfs"
	config.CgroupsPerQOS = ptr.To(true)

	configBytes, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("can't marshal kubelet config: %w", err)
	}

	err = os.WriteFile(path, configBytes, 0644)
	if err != nil {
		return fmt.Errorf("failed to write kubelet config: %w", err)
	}

	return nil
}

// determineKubeletResolvConfPath returns the path to the resolv.conf file that
// the kubelet should use.
func determineKubeletResolvConfPath() *string {
	path := "/etc/resolv.conf"

	switch runtime.GOOS {
	case "windows":
		return nil

	case "linux":
		// https://www.freedesktop.org/software/systemd/man/systemd-resolved.service.html#/etc/resolv.conf
		// If it's likely that resolv.conf is pointing to a systemd-resolved
		// nameserver, that nameserver won't be reachable from within
		// containers. Try to use the alternative resolv.conf path used by
		// systemd-resolved instead.
		detected, err := hasSystemdResolvedNameserver(path)
		if err != nil {
			logrus.WithError(err).Info("Failed to detect the presence of systemd-resolved")
		} else if detected {
			systemdPath := "/run/systemd/resolve/resolv.conf"
			logrus.Infof("The file %s looks like it's managed by systemd-resolved, using resolv.conf: %s", path, systemdPath)
			return &systemdPath
		}
	}

	logrus.Infof("Using resolv.conf: %s", path)
	return &path
}

// hasSystemdResolvedNameserver parses the given resolv.conf file and checks if
// it contains 127.0.0.53 as the only nameserver. Then it is assumed to be
// systemd-resolved managed.
func hasSystemdResolvedNameserver(resolvConfPath string) (bool, error) {
	f, err := os.Open(resolvConfPath)
	if err != nil {
		return false, err
	}

	defer f.Close()

	// This is roughly how glibc and musl do it: check for "nameserver" followed
	// by whitespace, then try to parse the next bytes as IP address,
	// disregarding anything after any additional whitespace.
	// https://sourceware.org/git/?p=glibc.git;a=blob;f=resolv/res_init.c;h=cce842fa9311c5bdba629f5e78c19746f75ef18e;hb=refs/tags/glibc-2.37#l396
	// https://git.musl-libc.org/cgit/musl/tree/src/network/resolvconf.c?h=v1.2.3#n62

	nameserverLine := regexp.MustCompile(`^nameserver\s+(\S+)`)

	lines := bufio.NewScanner(f)
	systemdResolvedIPSeen := false
	for lines.Scan() {
		match := nameserverLine.FindSubmatch(lines.Bytes())
		if len(match) < 1 {
			continue
		}
		ip := net.ParseIP(string(match[1]))
		if ip == nil {
			continue
		}
		if systemdResolvedIPSeen || !ip.Equal(net.IP{127, 0, 0, 53}) {
			return false, nil
		}
		systemdResolvedIPSeen = true
	}
	if err := lines.Err(); err != nil {
		return false, err
	}

	return systemdResolvedIPSeen, nil
}

package node

import (
	"bufio"
	"context"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"github.com/ghodss/yaml"
	"github.com/loft-sh/vcluster/pkg/certs"
	"github.com/loft-sh/vcluster/pkg/k8s/command"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	certutil "k8s.io/client-go/util/cert"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	kubeletv1beta1 "k8s.io/kubelet/config/v1beta1"
)

const (
	KubeletDataDir     = "/data/kubelet/root"
	KubeletCertDir     = "/data/kubelet/pki"
	KubeletTLSCertFile = "/data/kubelet/pki/kubelet-serving.crt"
	KubeletTLSKeyFile  = "/data/kubelet/pki/kubelet-serving.key"
	KubeletKubeConfig  = "/data/kubelet/pki/kube-config.yaml"
	KubeletConfig      = "/data/kubelet/kubelet-config.yaml"
)

func StartKubelet(ctx context.Context, eg *errgroup.Group, caCertPath, nodeName, podCIDR, clusterDomain string) error {
	// write kubelet certs
	err := writeKubeletCerts(nodeName, caCertPath)
	if err != nil {
		return err
	}

	// get cluster dns
	clusterDNS, err := waitForClusterDNS(ctx)
	if err != nil {
		return err
	}

	// write kubelet config
	err = writeKubeletConfig(ctx, KubeletConfig, caCertPath, podCIDR, clusterDomain, clusterDNS)
	if err != nil {
		return fmt.Errorf("write kubelet config: %w", err)
	}

	// start kubelet command
	eg.Go(func() error {
		err := os.MkdirAll(KubeletDataDir, 0755)
		if err != nil {
			return fmt.Errorf("make kubelet dir: %w", err)
		}

		// build flags
		args := []string{}
		args = append(args, "/usr/local/bin/kubelet")
		args = append(args, "--root-dir="+KubeletDataDir)
		args = append(args, "--config="+KubeletConfig)
		args = append(args, "--kubeconfig="+KubeletKubeConfig)
		args = append(args, "--containerd="+containerdSock)
		args = append(args, "--hostname-override="+nodeName)

		// runtime cgroups
		_, runtimeRoot, _ := CheckCgroups()
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

func waitForClusterDNS(ctx context.Context) (string, error) {
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

	serviceIP := ""
	err = wait.PollUntilContextCancel(ctx, time.Second*2, true, func(ctx context.Context) (done bool, err error) {
		service, err := kubeClient.CoreV1().Services("kube-system").Get(ctx, "kube-dns", metav1.GetOptions{})
		if err != nil {
			klog.V(1).Info("Couldn't get kube-dns for kubelet", "error", err)
			return false, nil
		} else if service.Spec.ClusterIP != "" {
			serviceIP = service.Spec.ClusterIP
			return true, nil
		}

		klog.V(1).Info("Waiting for kube-dns service to receive cluster ip")
		return false, nil
	})
	if err != nil {
		return "", fmt.Errorf("wait for pod cidr: %w", err)
	}

	klog.Info("Kube DNS found")
	return serviceIP, nil
}

func writeKubeletCerts(nodeName, caCertPath string) error {
	_, err := os.Stat(KubeletKubeConfig)
	if err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	err = os.MkdirAll(KubeletCertDir, 0755)
	if err != nil {
		return fmt.Errorf("make kubelet cert dir: %w", err)
	}

	// create kube-config
	err = certs.CreateKubeConfigFiles(KubeletCertDir, map[string]*certs.KubeConfigSpec{
		filepath.Base(KubeletKubeConfig): {
			APIServer:     "https://127.0.0.1:6443",
			Organizations: []string{"system:nodes"},
			ClientName:    "system:node:" + string(nodeName),
		},
	}, filepath.Dir(caCertPath), "vcluster")
	if err != nil {
		return fmt.Errorf("create kubelet kube config: %w", err)
	}

	// create serving certs
	caCert, caKey, err := certs.TryLoadCertAndKeyFromDisk(filepath.Dir(caCertPath), certs.CACertAndKeyBaseName)
	if err != nil {
		return fmt.Errorf("couldn't create a kubeconfig; the CA files couldn't be loaded: %w", err)
	}
	cert := &certs.KubeadmCert{
		Name:     "kubelet-serving",
		LongName: "certificate for serving the kubelet",
		BaseName: "kubelet-serving",
		CAName:   "ca",
		Config: certs.CertConfig{
			Config: certutil.Config{
				CommonName: nodeName,
				AltNames: certutil.AltNames{
					DNSNames: []string{nodeName, "localhost"},
				},
				Usages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			},
		},
	}
	err = cert.CreateFromCAIn(KubeletCertDir, caCert, caKey)
	if err != nil {
		return fmt.Errorf("create kubelet serving cert: %w", err)
	}

	return nil
}

func writeKubeletConfig(ctx context.Context, path, caCertPath, podCIDR, clusterDomain, clusterDNS string) error {
	config := &kubeletv1beta1.KubeletConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: kubeletv1beta1.SchemeGroupVersion.String(),
			Kind:       "KubeletConfiguration",
		},
	}

	// figure out cgroups
	kubeletRoot, _, controllers := CheckCgroups()
	if !controllers["cpu"] {
		klog.FromContext(ctx).Info("Disabling CPU quotas due to missing cpu controller or cpu.cfs_period_us")
		config.CPUCFSQuota = ptr.To(false)
	}
	if !controllers["pids"] {
		klog.Fatal("pids cgroup controller not found")
	}
	if kubeletRoot != "" {
		config.KubeletCgroups = kubeletRoot
	}
	config.Authentication.X509.ClientCAFile = caCertPath
	config.Authentication.Anonymous.Enabled = ptr.To(false)
	config.Authentication.Webhook.Enabled = ptr.To(true)
	config.Authorization.Mode = kubeletv1beta1.KubeletAuthorizationModeWebhook
	config.ReadOnlyPort = 0
	config.PodCIDR = podCIDR
	config.ClusterDomain = clusterDomain
	config.ClusterDNS = []string{clusterDNS}
	config.TLSCertFile = KubeletTLSCertFile
	config.TLSPrivateKeyFile = KubeletTLSKeyFile
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

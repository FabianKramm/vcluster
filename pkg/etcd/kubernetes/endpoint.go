package kubernetes

import (
	"context"
	"crypto/tls"
	"net"
	"os"
	"strings"
	"time"

	"github.com/loft-sh/vcluster/pkg/etcd/kubernetes/backend"
	"github.com/loft-sh/vcluster/pkg/etcd/kubernetes/server"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.etcd.io/etcd/server/v3/embed"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

const (
	ListenerSocket = "unix://vcluster.sock"
)

type Config struct {
	Listener            string
	NotifyInterval      time.Duration
	EmulatedETCDVersion string
}

type ETCDConfig struct {
	Endpoints   []string
	TLSConfig   tls.Config
	LeaderElect bool
}

func Listen(ctx context.Context, config Config) (ETCDConfig, error) {
	if config.EmulatedETCDVersion == "" {
		config.EmulatedETCDVersion = "3.5.13"
	}
	if config.NotifyInterval == 0 {
		config.NotifyInterval = time.Second * 5
	}

	// start the backend
	backend := backend.NewBackend(backend.NewStorage(backend.NewMemoryPageCache()))
	if err := backend.Start(ctx); err != nil {
		return ETCDConfig{}, errors.Wrap(err, "starting kine backend")
	}

	// set up GRPC server and register services
	b := server.New(backend, endpointScheme(config), config.NotifyInterval, config.EmulatedETCDVersion)
	grpcServer, err := grpcServer()
	if err != nil {
		return ETCDConfig{}, errors.Wrap(err, "creating GRPC server")
	}
	b.Register(grpcServer)

	// Create raw listener and wrap in cmux for protocol switching
	listener, err := createListener(config)
	if err != nil {
		return ETCDConfig{}, errors.Wrap(err, "creating listener")
	}

	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			logrus.Errorf("Kine GPRC server exited: %v", err)
		}
	}()

	endpoint := endpointURL(config, listener)
	logrus.Infof("Kine available at %s", endpoint)

	return ETCDConfig{
		LeaderElect: false,
		Endpoints:   []string{endpoint},
	}, nil
}

// endpointURL returns a URI string suitable for use as a local etcd endpoint.
// For TCP sockets, it is assumed that the port can be reached via the loopback address.
func endpointURL(config Config, listener net.Listener) string {
	scheme := endpointScheme(config)
	address := listener.Addr().String()
	if !strings.HasPrefix(scheme, "unix") {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			logrus.Warnf("failed to get listener port: %v", err)
			port = "2379"
		}
		address = "127.0.0.1:" + port
	}

	return scheme + "://" + address
}

// endpointScheme returns the URI scheme for the listener specified by the configuration.
func endpointScheme(config Config) string {
	if config.Listener == "" {
		config.Listener = ListenerSocket
	}

	scheme, _ := SchemeAndAddress(config.Listener)
	if scheme != "unix" {
		scheme = "http"
	}

	return scheme
}

// createListener returns a listener bound to the requested protocol and address.
func createListener(config Config) (ret net.Listener, rerr error) {
	if config.Listener == "" {
		config.Listener = ListenerSocket
	}
	scheme, address := SchemeAndAddress(config.Listener)

	if scheme == "unix" {
		if err := os.Remove(address); err != nil && !os.IsNotExist(err) {
			logrus.Warnf("failed to remove socket %s: %v", address, err)
		}
		defer func() {
			if err := os.Chmod(address, 0600); err != nil {
				rerr = err
			}
		}()
	} else {
		scheme = "tcp"
	}

	return net.Listen(scheme, address)
}

// grpcServer returns either a preconfigured GRPC server, or builds a new GRPC
// server using upstream keepalive defaults plus the local Server TLS configuration.
func grpcServer() (*grpc.Server, error) {
	gopts := []grpc.ServerOption{
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             embed.DefaultGRPCKeepAliveMinTime,
			PermitWithoutStream: false,
		}),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    embed.DefaultGRPCKeepAliveInterval,
			Timeout: embed.DefaultGRPCKeepAliveTimeout,
		}),
	}

	return grpc.NewServer(gopts...), nil
}

// SchemeAndAddress crudely splits a URL string into scheme and address,
// where the address includes everything after the scheme/authority separator.
func SchemeAndAddress(str string) (string, string) {
	parts := strings.SplitN(str, "://", 2)
	if len(parts) > 1 {
		return parts[0], parts[1]
	}
	return "", parts[0]
}

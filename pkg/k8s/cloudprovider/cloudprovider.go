package cloudprovider

import (
	"context"
	"time"
)

type CloudProvider struct {
	config         Config
	stopCh         chan struct{}
	commandBuilder CommandBuilder
}

// CommandBuilder allows for defining arbitrary functions that can
// create `Command` instances.
type CommandBuilder func() (Command, error)

// NewCloudProvider creates a new vcluster cloud-provider using the default
// address collector and command.
func NewCloudProvider(kubeConfigPath string, frequency time.Duration, port int) *CloudProvider {
	config := Config{
		AddressCollector: DefaultAddressCollector(),
		KubeConfig:       kubeConfigPath,
		UpdateFrequency:  frequency,
		BindPort:         port,
	}

	return newCloudProvider(config, func() (Command, error) {
		return NewCommand(config)
	})
}

// newCloudProvider is a helper for creating specialized vcluster-cloud-provider
// instances that can be used for testing.
func newCloudProvider(config Config, cb CommandBuilder) *CloudProvider {
	return &CloudProvider{
		config:         config,
		stopCh:         make(chan struct{}),
		commandBuilder: cb,
	}
}

// Run will create a k0s-cloud-provider command, and run it on a goroutine.
// Failures to create this command will be returned as an error.
func (c *CloudProvider) Start(_ context.Context) error {
	command, err := c.commandBuilder()
	if err != nil {
		return err
	}

	go command(c.stopCh)

	return nil
}

// Stop will stop the k0s-cloud-provider command goroutine (if running)
func (c *CloudProvider) Stop() error {
	close(c.stopCh)

	return nil
}

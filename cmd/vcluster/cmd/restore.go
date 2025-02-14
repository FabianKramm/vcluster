package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	vclusterconfig "github.com/loft-sh/vcluster/config"
	"github.com/loft-sh/vcluster/pkg/config"
	"github.com/loft-sh/vcluster/pkg/constants"
	"github.com/loft-sh/vcluster/pkg/etcd"
	"github.com/loft-sh/vcluster/pkg/snapshot"
	"github.com/spf13/cobra"
	"k8s.io/klog/v2"
)

type RestoreOptions struct {
	Snapshot snapshot.Options
}

func NewRestoreCommand() *cobra.Command {
	options := &RestoreOptions{}
	envOptions, err := parseOptionsFromEnv()
	if err != nil {
		klog.Warningf("Error parsing environment variables: %v", err)
	} else {
		options.Snapshot = *envOptions
	}

	cmd := &cobra.Command{
		Use:   "restore",
		Short: "restore a vCluster",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return options.Run(cmd.Context())
		},
	}

	// add storage flags
	snapshot.AddFlags(cmd.Flags(), &options.Snapshot)
	return cmd
}

func (o *RestoreOptions) Run(ctx context.Context) error {
	// parse vCluster config
	vConfig, err := config.ParseConfig(constants.DefaultVClusterConfigLocation, os.Getenv("VCLUSTER_NAME"), nil)
	if err != nil {
		return err
	}

	// make sure to validate options
	err = validateOptions(vConfig, &o.Snapshot, true)
	if err != nil {
		return err
	}

	// create new etcd client
	etcdClient, err := newRestoreEtcdClient(ctx, vConfig)
	if err != nil {
		return fmt.Errorf("failed to create etcd client: %w", err)
	}

	// create store
	objectStore, err := createStore(ctx, &o.Snapshot)
	if err != nil {
		return fmt.Errorf("failed to create store: %w", err)
	}

	// now stream objects from object store to etcd
	reader, err := objectStore.GetObject(ctx)
	if err != nil {
		return fmt.Errorf("failed to get backup: %w", err)
	}
	defer reader.Close()

	// print log message that we start restoring
	klog.Infof("Start restoring etcd snapshot from %s...", objectStore.Target())

	// optionally decompress
	gzipReader, err := gzip.NewReader(reader)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzipReader.Close()

	// create a new tar reader
	tarReader := tar.NewReader(gzipReader)

	// now restore each key value
	restoredKeys := 0
	for {
		// read from archive
		key, value, err := readKeyValue(tarReader)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read etcd key/value: %w", err)
		} else if errors.Is(err, io.EOF) || len(key) == 0 {
			break
		}

		// write the value to etcd
		klog.V(1).Infof("Restore key %s", string(key))
		err = etcdClient.Put(ctx, string(key), value)
		if err != nil {
			return fmt.Errorf("restore etcd key %s: %w", string(key), err)
		}

		// print status update
		restoredKeys++
		if restoredKeys%100 == 0 {
			klog.Infof("Restored %d keys", restoredKeys)
		}
	}
	klog.Infof("Successfully restored %d etcd keys from snapshot", restoredKeys)
	klog.Infof("Successfully restored snapshot from %s", objectStore.Target())

	return nil
}

func newRestoreEtcdClient(ctx context.Context, vConfig *config.VirtualClusterConfig) (etcd.Client, error) {
	// delete existing storage:
	// * embedded etcd: just delete the files locally
	// * deploy etcd: range delete request
	// * embedded database: just delete the files locally
	// * external database: we can't so we skip and then check later if there are any already
	if vConfig.BackingStoreType() == vclusterconfig.StoreTypeEmbeddedDatabase {
		if vConfig.Distro() == vclusterconfig.K8SDistro {
			// this is a little bit stupid since we cannot rename /data, so we have to snapshot the
			// individual file.
			err := backupFile(ctx, constants.K8sSqliteDatabase)
			if err != nil {
				return nil, err
			}
			_ = os.RemoveAll(constants.K8sSqliteDatabase + "-wal")
			_ = os.RemoveAll(constants.K8sSqliteDatabase + "-shm")
		} else if vConfig.Distro() == vclusterconfig.K3SDistro {
			err := backupFolder(ctx, filepath.Dir(constants.K3sSqliteDatabase))
			if err != nil {
				return nil, err
			}
		}
	} else if vConfig.BackingStoreType() == vclusterconfig.StoreTypeEmbeddedEtcd {
		embeddedEtcdData := "/data/etcd"
		err := backupFolder(ctx, embeddedEtcdData)
		if err != nil {
			return nil, err
		}
	}

	// now create the etcd client
	etcdClient, err := newEtcdClient(ctx, vConfig, true)
	if err != nil {
		return nil, err
	}

	// delete contents in external etcd
	if vConfig.BackingStoreType() == vclusterconfig.StoreTypeExternalEtcd {
		klog.FromContext(ctx).Info("Delete existing etcd data before restore...")
		err = etcdClient.DeletePrefix(ctx, "/")
		if err != nil {
			return nil, err
		}
	}

	return etcdClient, nil
}

func backupFile(ctx context.Context, file string) error {
	_, err := os.Stat(file)
	if os.IsNotExist(err) {
		return nil
	}

	backupName := file + ".backup"
	_, err = os.Stat(backupName)
	if err == nil {
		_ = os.RemoveAll(backupName)
	}

	klog.FromContext(ctx).Info(fmt.Sprintf("Renaming existing database from %s to %s, if something goes wrong please restore the old database", file, backupName))
	return os.Rename(file, backupName)
}

func backupFolder(ctx context.Context, dir string) error {
	_, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return nil
	}

	backupName := dir + ".backup"
	_, err = os.Stat(backupName)
	if err == nil {
		_ = os.RemoveAll(backupName)
	}

	klog.FromContext(ctx).Info(fmt.Sprintf("Renaming existing database from %s to %s, if something goes wrong please restore the old database", dir, backupName))
	err = os.Rename(dir, backupName)
	if err != nil {
		return err
	}

	return os.MkdirAll(dir, 0777)
}

func readKeyValue(tarReader *tar.Reader) ([]byte, []byte, error) {
	header, err := tarReader.Next()
	if err != nil {
		return nil, nil, err
	}

	buf := &bytes.Buffer{}
	_, err = io.Copy(buf, tarReader)
	if err != nil {
		return nil, nil, err
	}

	return []byte(header.Name), buf.Bytes(), nil
}

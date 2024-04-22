package cmd

import (
	"fmt"
	"os"

	loftctl "github.com/loft-sh/loftctl/v3/cmd/loftctl/cmd"
	"github.com/loft-sh/log"
	"github.com/loft-sh/vcluster/config"
	"github.com/loft-sh/vcluster/pkg/cli/flags"
	"github.com/loft-sh/vcluster/pkg/platform"
	"github.com/spf13/cobra"
)

func NewLogoutCmd(globalFlags *flags.GlobalFlags) (*cobra.Command, error) {
	loftctlGlobalFlags, err := platform.GlobalFlags(globalFlags)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pro flags: %w", err)
	}

	cmd := &loftctl.LogoutCmd{
		GlobalFlags: loftctlGlobalFlags,
		Log:         log.GetInstance(),
	}

	description := `########################################################
################### vcluster logout ####################
########################################################
Log out of vCluster.Pro

Example:
vcluster logout
########################################################
	`

	logoutCmd := &cobra.Command{
		Use:   "logout",
		Short: "Log out of a vCluster.Pro instance",
		Long:  description,
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			if config.ShouldCheckForProFeatures() {
				cmd.Log.Warnf("In order to use a Pro feature, please contact us at https://www.vcluster.com/pro-demo or downgrade by running `vcluster upgrade --version v0.19.5`")
				os.Exit(1)
			}

			_, err := platform.CreatePlatformClient()
			if err != nil {
				return err
			}

			return cmd.RunLogout(cobraCmd.Context(), args)
		},
	}

	return logoutCmd, nil
}

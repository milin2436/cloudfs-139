// Command cloudfs is a command line client for China Mobile 139 cloud drive.
package main

import (
	"fmt"
	"os"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/milin2436/cloudfs-139/sdk/cloudfs"
)

var (
	// configPath is bound to -c/--config. An empty value resolves to
	// ~/.cloudfs.json inside cloudfs.ResolveConfigPath.
	configPath string
	verbose    bool
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

const longHelp = `cloudfs is a command line client for China Mobile 139 cloud drive (yun.139.com),
built on the OpenList 4.2.5 driver framework.

Accounts are configured in a JSON file (default ~/.cloudfs.json) and mounted
under a path prefix. Every command takes a path starting with that prefix, so
"cloudfs ls /yun139" lists one mount and "cloudfs ls /" lists the mounts.`

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "cloudfs",
		Short:         "China Mobile 139 cloud drive client",
		Long:          longHelp,
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if verbose {
				log.SetLevel(log.DebugLevel)
			}
			return nil
		},
	}
	root.PersistentFlags().StringVarP(
		&configPath, "config", "c", "",
		"config file path (default ~/"+cloudfs.DefaultConfigFileName+")")
	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable debug logging")

	root.AddCommand(
		newLsCmd(),
		newGetCmd(),
		newPutCmd(),
		newMkdirCmd(),
		newRmCmd(),
		newMvCmd(),
		newCpCmd(),
		newRenameCmd(),
		newSpaceCmd(),
		newWhoamiCmd(),
		newShareCmd(),
		newSharesCmd(),
		newUnshareCmd(),
		newInitCmd(),
	)
	return root
}

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/milin2436/cloudfs-139/sdk/cloudfs"
)

// lsCmdOutput formats ls output.
const (
	lsOutputTable = "table"
	lsOutputJSON  = "json"
	lsOutputRaw   = "raw"
)

func printFileTable(w io.Writer, files []cloudfs.File) {
	for _, f := range files {
		fmt.Fprintf(w, "%-6s %10s  %s  %s\n",
			f.Type, humanSize(f.Size), f.ModTime.Format("2006-01-02 15:04"), f.Name)
	}
}

func newLsCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "ls [path]",
		Short: "List a directory (defaults to /, the virtual root)",
		Args:  atMost(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := newClient(ctx)
			if err != nil {
				return err
			}

			// Capture HTTP traffic before the request is issued.
			if output == lsOutputRaw {
				cloudfs.EnableHTTPCapture()
				defer cloudfs.DisableHTTPCapture()
			}

			files, err := c.List(ctx, firstArg(args, "/"))
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			switch output {
			case lsOutputTable:
				printFileTable(out, files)
			case lsOutputJSON:
				data, err := json.MarshalIndent(files, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(out, string(data))
			case lsOutputRaw:
				printFileTable(out, files)
				if raw := cloudfs.FormatHTTPCaptures(); raw != "" {
					fmt.Fprintln(out)
					fmt.Fprintln(out, raw)
				}
			default:
				return fmt.Errorf("unknown --output %q (table, json, raw)", output)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&output, "output", lsOutputTable, "output format: table, json, raw")
	return cmd
}

func newGetCmd() *cobra.Command {
	var dest string
	cmd := &cobra.Command{
		Use:   "get <path>",
		Short: "Download a file (to stdout unless -o is given)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := newClient(ctx)
			if err != nil {
				return err
			}
			out, err := NewWriter(dest)
			if err != nil {
				return err
			}
			n, err := c.Download(ctx, args[0], out)
			if closeErr := out.Close(); err == nil && closeErr != nil {
				err = fmt.Errorf("close %q: %w", dest, closeErr)
			}
			if err != nil {
				return err
			}
			if dest != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "wrote %s (%d bytes) to %s\n", humanSize(n), n, dest)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&dest, "output", "o", "", "destination file (default stdout)")
	return cmd
}

func newPutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "put <remote-dir> <local-file>",
		Short: "Upload a local file into a remote directory",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd.Context())
			if err != nil {
				return err
			}
			if err := c.Upload(cmd.Context(), args[0], args[1], nil); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "uploaded "+filepath.Base(args[1]))
			return nil
		},
	}
}

func newMkdirCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mkdir <path>",
		Short: "Create a directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd.Context())
			if err != nil {
				return err
			}
			return c.MakeDir(cmd.Context(), args[0])
		},
	}
}

func newRmCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "rm <path>",
		Short: "Delete a file or directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := newClient(ctx)
			if err != nil {
				return err
			}
			if !force && !confirm(fmt.Sprintf("delete %s? [y/N] ", args[0])) {
				fmt.Fprintln(cmd.OutOrStdout(), "skipped")
				return nil
			}
			if err := c.Remove(ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "deleted "+args[0])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "delete without confirmation")
	return cmd
}

func newMvCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mv <src> <dst-dir>",
		Short: "Move a file or directory",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd.Context())
			if err != nil {
				return err
			}
			return c.Move(cmd.Context(), args[0], args[1])
		},
	}
}

func newCpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cp <src> <dst-dir>",
		Short: "Copy a file or directory",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd.Context())
			if err != nil {
				return err
			}
			return c.Copy(cmd.Context(), args[0], args[1])
		},
	}
}

func newRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <path> <new-name>",
		Short: "Rename the last segment of a path",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(cmd.Context())
			if err != nil {
				return err
			}
			return c.Rename(cmd.Context(), args[0], args[1])
		},
	}
}

func newSpaceCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "space [mount]",
		Short: "Show the quota of a mount (defaults to default_mount)",
		Long: `Show the quota of a mount.

For 139Yun this prints 139's own quota report in MB with a GiB column, broken
down by drive type (个人云 / 家庭云 / 共享群 / ...). Drive type is the finest
scope 139 reports: there is no per-directory quota, so nothing here is keyed by
a path. An account that is over quota shows a negative freeDiskSize.

Other drivers fall back to plain total / used / free in bytes.`,
		Args: atMost(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := newClient(ctx)
			if err != nil {
				return err
			}
			mount, err := c.ResolveMountPath(firstArg(args, ""))
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			// Prefer 139's own report: it is the same single request Space()
			// would make, and it also carries the per-drive-type breakdown.
			// Drivers that do not implement it fall back to the plain numbers.
			q, qerr := c.Quota(ctx, mount)
			var total, used, free int64
			if qerr == nil {
				total, used, free = q.TotalBytes(), q.UsedBytes(), q.FreeBytes()
			} else {
				sp, err := c.Space(ctx, mount)
				if err != nil {
					return err
				}
				total, used, free = sp.Total, sp.Used, sp.Free
			}

			if asJSON {
				// Byte totals stay at the top level so an existing consumer of
				// total / used / free keeps working; quota is additive.
				env := struct {
					Mount string         `json:"mount"`
					Total int64          `json:"total"`
					Used  int64          `json:"used"`
					Free  int64          `json:"free"`
					Quota *cloudfs.Quota `json:"quota,omitempty"`
				}{Mount: mount, Total: total, Used: used, Free: free}
				if qerr == nil {
					env.Quota = q
				}
				data, err := json.Marshal(env)
				if err != nil {
					return err
				}
				fmt.Fprintln(out, string(data))
				return nil
			}

			if qerr == nil {
				fmt.Fprint(out, quotaTable(q, mount))
				return nil
			}
			fmt.Fprintf(out, "mount: %s\n", mount)
			fmt.Fprintf(out, "total: %s (%d bytes)\n", humanSize(total), total)
			fmt.Fprintf(out, "used:  %s (%d bytes)\n", humanSize(used), used)
			fmt.Fprintf(out, "free:  %s (%d bytes)\n", humanSize(free), free)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON instead of a summary")
	return cmd
}

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami [mount]",
		Short: "Show the account bound to a mount (defaults to default_mount)",
		Args:  atMost(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := newClient(ctx)
			if err != nil {
				return err
			}
			info, err := c.UserInfo(ctx, firstArg(args, ""))
			if err != nil {
				return err
			}
			data, err := json.Marshal(info)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return nil
		},
	}
}

func newInitCmd() *cobra.Command {
	var (
		driverName string
		write      bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Print or write a sample config file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := cloudfs.SampleConfig(driverName).Marshal()
			if err != nil {
				return err
			}
			var buf bytes.Buffer
			if err := json.Indent(&buf, data, "", "  "); err != nil {
				return err
			}
			pretty := buf.Bytes()
			if !write {
				fmt.Fprintln(cmd.OutOrStdout(), string(pretty))
				return nil
			}

			path, err := cloudfs.ResolveConfigPath(configPath)
			if err != nil {
				return err
			}
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("%s already exists; edit it in place instead of overwriting it", path)
			} else if !os.IsNotExist(err) {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			// 0600: the config holds live tokens.
			if err := os.WriteFile(path, pretty, 0o600); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote sample config to %s (mode 0600)\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&driverName, "driver", "",
		"driver name to put in the sample config (default "+cloudfs.DriverYun139+")")
	cmd.Flags().BoolVar(&write, "write", false,
		"write to the config path instead of printing to stdout")
	return cmd
}

package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/milin2436/cloudfs-139/sdk/cloudfs"
)

// 139 only accepts these validity windows.
func validShareDays(d int) bool {
	return d == 0 || d == 1 || d == 7
}

func shareLabel(days int) string {
	switch days {
	case 0:
		return "forever"
	case 1:
		return "1 day"
	case 7:
		return "7 days"
	default:
		return fmt.Sprintf("%d days", days)
	}
}

func newShareCmd() *cobra.Command {
	var (
		encrypted bool
		days      int
		name      string
		viewers   string
		asJSON    bool
	)
	cmd := &cobra.Command{
		Use:   "share <path> [--password] [--days N] [--name NAME] [--to p1,p2]",
		Short: "Create a share link for a file or directory (this publishes it)",
		Long: `Create a 139 share link ("外链") for one file or directory.

Anyone with the URL can download the object, so this command publishes
content. Validity is "never expires" and there is no extract code unless
asked for.

--password turns on an extract code, which 139 generates itself; the code is
printed with the link. --days only accepts 0 (never expires), 1 or 7 — that
is the full set 139 offers. --to takes a comma separated list of phone
numbers and makes the link friend-only.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := newClient(ctx)
			if err != nil {
				return err
			}
			if !validShareDays(days) {
				return fmt.Errorf("--days must be 0, 1 or 7, got %d", days)
			}

			viewersList := splitCSV(viewers)
			opts := cloudfs.ShareOptions{
				Encrypted:   encrypted,
				ExpiresDays: days,
				Name:        name,
				PubType:     1,
				Viewers:     viewersList,
			}
			if len(viewersList) > 0 {
				opts.PubType = 2
			}

			res, err := c.Share(ctx, args[0], opts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				data, err := json.Marshal(res)
				if err != nil {
					return err
				}
				fmt.Fprintln(out, string(data))
				return nil
			}

			fmt.Fprintf(out, "shared: %s\n", args[0])
			fmt.Fprintf(out, "link:   %s\n", res.URL)
			if res.Passwd != "" {
				fmt.Fprintf(out, "code:   %s\n", res.Passwd)
			}
			fmt.Fprintf(out, "valid:  %s\n", shareLabel(days))
			copyable := res.URL
			if res.Passwd != "" {
				copyable = res.URL + "  提取码: " + res.Passwd
			}
			fmt.Fprintf(out, "copy:   链接: %s\n", copyable)
			return nil
		},
	}
	cmd.Flags().BoolVar(&encrypted, "password", false,
		"require an extract code (139 generates the code; it is printed with the link)")
	cmd.Flags().IntVar(&days, "days", 0, "validity in days: 0 never expires, 1, or 7 (default 0)")
	cmd.Flags().StringVarP(&name, "name", "n", "", "link display name (139 blocks sensitive words)")
	cmd.Flags().StringVar(&viewers, "to", "", "comma separated phone numbers; makes the link friend-only")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON instead of a summary")
	return cmd
}

func newSharesCmd() *cobra.Command {
	var (
		kind   string
		mount  string
		start  int
		limit  int
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "shares [--type sent|received] [--mount M]",
		Short: "List share links (defaults to shares you sent)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := newClient(ctx)
			if err != nil {
				return err
			}

			k := cloudfs.ShareKindSent
			switch kind {
			case "sent", "s", "":
			case "received", "r", "in", "inbound":
				k = cloudfs.ShareKindReceived
			default:
				return fmt.Errorf("unknown --type %q (sent, received)", kind)
			}

			page, err := c.ListShares(ctx, mount, k, start, limit)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				data, err := json.MarshalIndent(page, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(out, string(data))
				return nil
			}

			label := "shares you sent"
			if k == cloudfs.ShareKindReceived {
				label = "shares sent to you"
			}
			if len(page.Items) == 0 {
				fmt.Fprintf(out, "no %s\n", label)
				return nil
			}
			fmt.Fprintf(out, "%s: %d total, showing %d\n",
				label, page.Count, len(page.Items))
			fmt.Fprintf(out, "%-8s %-22s %-9s %-7s %-7s %-6s %s\n",
				"link", "name", "code", "pub", "dl", "valid", "created")
			for _, it := range page.Items {
				pub := "public"
				if it.FriendsOnly {
					pub = "friends"
				}
				expires := "forever"
				if it.Expires != "" {
					expires = it.Expires
				}
				fmt.Fprintf(out, "%-8s %-22s %-9s %-7s %-7d %-6s %s\n",
					truncate(it.LinkID, 8), truncate(it.Name, 22), orDash(it.Passwd),
					pub, it.Downloads, expires, it.Created)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "type", "sent", "which list: sent or received")
	cmd.Flags().StringVar(&mount, "mount", "", "mount path (default default_mount)")
	cmd.Flags().IntVar(&start, "start", 1, "first row of the page, 1-based")
	cmd.Flags().IntVar(&limit, "limit", 100, "number of rows to fetch")
	cmd.Flags().BoolVarP(&asJSON, "json", "j", false, "print JSON instead of a table")
	return cmd
}

func newUnshareCmd() *cobra.Command {
	var (
		mount string
		force bool
	)
	cmd := &cobra.Command{
		Use:   "unshare <linkID> [--mount M]",
		Short: "Revoke a share link you created",
		Long: `Cancel one of your own share links by its link id. Use "cloudfs shares" to
find the id. Only the link's creator can revoke it, and revoking it stops
further downloads immediately.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := newClient(ctx)
			if err != nil {
				return err
			}
			if !force && !confirm(fmt.Sprintf("revoke share %s? [y/N] ", args[0])) {
				fmt.Fprintln(cmd.OutOrStdout(), "skipped")
				return nil
			}
			if err := c.CancelShare(ctx, mount, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "revoked share %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&mount, "mount", "", "mount path that owns the link (default default_mount)")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "revoke without confirmation")
	return cmd
}

func splitCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// truncate shortens to n runes so a table column stays put with CJK names.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

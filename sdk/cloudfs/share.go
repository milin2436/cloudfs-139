package cloudfs

import (
	"context"
	"fmt"

	"github.com/milin2436/cloudfs-139/cloudfso/model"
	"github.com/milin2436/cloudfs-139/cloudfso/op"
	"github.com/milin2436/cloudfs-139/drivers/yun139"
)

// Listing an outlink list means "shares I sent" or "shares sent to me".
// Kind 0 is rejected by 139, so callers must pick one of these.
const (
	ShareKindSent     = 1
	ShareKindReceived = 2
)

// ShareOptions configures a share. Zero values give the most permissive share:
// public, no password, no expiry.
type ShareOptions struct {
	// Encrypted adds an extract code. 139 generates the code and returns it in
	// ShareResult.Passwd, so this cannot be set to a chosen value.
	Encrypted bool `json:"encrypted,omitempty"`
	// ExpiresDays is 0 (never expires), 1, or 7.
	ExpiresDays int `json:"expires_days,omitempty"`
	// Name is the link's display name. Empty uses 139's default.
	Name string `json:"name,omitempty"`
	// PubType is 1 (public) or 2 (friends only). Empty means public.
	PubType int `json:"pub_type,omitempty"`
	// Viewers are phone numbers; required when PubType is 2.
	Viewers []string `json:"viewers,omitempty"`
}

// ShareResult is a newly created share.
type ShareResult struct {
	// LinkID is the handle CancelShare takes.
	LinkID string `json:"link_id"`
	// URL is the link to hand to someone.
	URL string `json:"url"`
	// Passwd is the extract code, non-empty only for password-protected shares.
	Passwd string `json:"passwd,omitempty"`
}

// ShareInfo is one row of a share list.
type ShareInfo struct {
	LinkID    string `json:"link_id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Passwd    string `json:"passwd,omitempty"`
	Encrypted bool   `json:"encrypted"`
	// FriendsOnly is true when the link is visible only to the shared-with list.
	FriendsOnly bool   `json:"friends_only"`
	Downloads   int64  `json:"downloads"`
	Saves       int64  `json:"saves"`
	Browsing    int64  `json:"browsing"`
	Created     string `json:"created"`
	Expires     string `json:"expires,omitempty"`
	// Invalid is true when 139 has taken the link down.
	Invalid bool   `json:"invalid"`
	Owner   string `json:"owner,omitempty"`
	// SharedBy is the author, populated for received shares only.
	SharedBy string `json:"shared_by,omitempty"`
}

// SharePage is one page of ListShares.
type SharePage struct {
	Items []ShareInfo `json:"items"`
	Count int64       `json:"count"`
	// Open is 139's raw openCount. It is NOT a count of active links: a single
	// live, unexpired share was observed returning 0, so read it as whatever
	// 139 happens to send rather than a trustworthy tally.
	Open int64 `json:"open"`
	// Kind echoes ShareKindSent or ShareKindReceived.
	Kind int `json:"kind"`
}

// outlinkShareable is the shape *Yun139 exposes for outlinks.
//
// There is no framework-level interface for sharing, so like account info it is
// only discoverable by type assertion. That keeps the SDK generic while still
// supporting 139Yun.
type outlinkShareable interface {
	CreateOutLink(ctx context.Context, objs []model.Obj, opts yun139.ShareOptions) (*yun139.ShareResult, error)
	ListOutLinks(ctx context.Context, qryType, bNum, eNum int) (*yun139.ShareList, error)
	DeleteOutLinks(ctx context.Context, linkIDs []string) error
}

func (c *Client) shareableDriver(mount string) (outlinkShareable, error) {
	d, err := c.driverForMount(mount)
	if err != nil {
		return nil, err
	}
	sh, ok := d.(outlinkShareable)
	if !ok {
		return nil, fmt.Errorf(
			"storage %q uses %s, which does not support sharing", mount, d.Config().Name)
	}
	return sh, nil
}

// Share shares one object and returns the new link.
//
// This publishes the object to whoever receives the URL, so a caller should not
// invoke it without a human choosing what to share.
func (c *Client) Share(ctx context.Context, remotePath string, opts ShareOptions) (*ShareResult, error) {
	if err := c.ensureInit(ctx); err != nil {
		return nil, err
	}
	mount, rel, err := c.resolvePath(remotePath)
	if err != nil {
		return nil, err
	}
	d, err := c.driverForMount(mount)
	if err != nil {
		return nil, err
	}
	// Get falls back to listing the parent when the driver cannot stat directly,
	// which is how every non-share 139 account behaves.
	obj, err := op.Get(ctx, d, rel, true)
	if err != nil {
		return nil, fmt.Errorf("share %q: %w", remotePath, err)
	}
	if obj == nil {
		return nil, fmt.Errorf("share %q: 139 returned no object", remotePath)
	}

	sh, err := c.shareableDriver(mount)
	if err != nil {
		return nil, err
	}
	out, err := sh.CreateOutLink(ctx, []model.Obj{obj}, yun139.ShareOptions{
		Encrypted:   opts.Encrypted,
		ExpiresDays: opts.ExpiresDays,
		Name:        opts.Name,
		PubType:     opts.PubType,
		Viewers:     opts.Viewers,
	})
	if err != nil {
		return nil, fmt.Errorf("share %q: %w", remotePath, err)
	}
	if out == nil || out.LinkID == "" {
		return nil, fmt.Errorf("share %q: 139 returned no link id", remotePath)
	}
	return &ShareResult{LinkID: out.LinkID, URL: out.URL, Passwd: out.Passwd}, nil
}

// ListShares lists the account's shares. mountPath resolves like Space and
// UserInfo; kind must be ShareKindSent or ShareKindReceived. bNum and eNum are
// a 1-based inclusive range; non-positive values select the first page.
func (c *Client) ListShares(ctx context.Context, mountPath string, kind, bNum, eNum int) (*SharePage, error) {
	if err := c.ensureInit(ctx); err != nil {
		return nil, err
	}
	mount, err := c.mountPath(mountPath)
	if err != nil {
		return nil, err
	}
	if kind != ShareKindSent && kind != ShareKindReceived {
		return nil, fmt.Errorf("share kind must be %d (sent) or %d (received), got %d",
			ShareKindSent, ShareKindReceived, kind)
	}
	sh, err := c.shareableDriver(mount)
	if err != nil {
		return nil, err
	}
	list, err := sh.ListOutLinks(ctx, kind, bNum, eNum)
	if err != nil {
		return nil, fmt.Errorf("storage %q: %w", mount, err)
	}
	if list == nil {
		return nil, fmt.Errorf("storage %q returned no share list", mount)
	}
	items := make([]ShareInfo, 0, len(list.Items))
	for _, in := range list.Items {
		items = append(items, ShareInfo{
			LinkID:      in.LinkID,
			Name:        in.Name,
			URL:         in.URL,
			Passwd:      in.Passwd,
			Encrypted:   in.Encrypt != 0,
			FriendsOnly: in.PubType == 2,
			Downloads:   in.DownloadTimes,
			Saves:       in.SaveTimes,
			Browsing:    in.BrowseTimes,
			Created:     in.CreateTime,
			Expires:     in.ExpireTime,
			Invalid:     in.Invalid,
			Owner:       in.Owner,
			SharedBy:    in.ShareAuthor,
		})
	}
	return &SharePage{Items: items, Count: list.Count, Open: list.Open, Kind: list.QryType}, nil
}

// CancelShare revokes one share. mountPath selects the account that owns the
// link; only the creator can revoke it.
func (c *Client) CancelShare(ctx context.Context, mountPath, linkID string) error {
	if err := c.ensureInit(ctx); err != nil {
		return err
	}
	if linkID == "" {
		return fmt.Errorf("share %q: link id is empty", linkID)
	}
	mount, err := c.mountPath(mountPath)
	if err != nil {
		return err
	}
	sh, err := c.shareableDriver(mount)
	if err != nil {
		return err
	}
	if err := sh.DeleteOutLinks(ctx, []string{linkID}); err != nil {
		return fmt.Errorf("storage %q: %w", mount, err)
	}
	return nil
}

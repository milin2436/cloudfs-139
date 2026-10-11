package cloudfs

import (
	"context"
	"fmt"

	"github.com/milin2436/cloudfs-139/drivers/yun139"
)

// userInfoGettable is the shape *Yun139 exposes.
//
// There is deliberately no framework-level interface for account info:
// cloudfso/driver defines no such capability, so it can only be discovered by
// type assertion. This keeps the SDK generic while still supporting 139Yun.
type userInfoGettable interface {
	GetUserInfo(ctx context.Context) (*yun139.UserInfo, error)
}

// UserInfo returns the account bound to a mount, or to default_mount when
// mountPath is empty. Fields a driver cannot resolve stay zero-valued.
func (c *Client) UserInfo(ctx context.Context, mountPath string) (*UserInfo, error) {
	if err := c.ensureInit(ctx); err != nil {
		return nil, err
	}
	mount, err := c.mountPath(mountPath)
	if err != nil {
		return nil, err
	}
	d, err := c.driverForMount(mount)
	if err != nil {
		return nil, err
	}

	getter, ok := d.(userInfoGettable)
	if !ok {
		return nil, fmt.Errorf(
			"storage %q uses %s, which does not expose account info", mount, d.Config().Name)
	}
	info, err := getter.GetUserInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("storage %q: %w", mount, err)
	}
	if info == nil {
		return nil, fmt.Errorf("storage %q returned no user info", mount)
	}

	return &UserInfo{
		Account:      info.Account,
		Username:     info.Username,
		UserDomainID: info.UserDomainID,
		Nickname:     info.Nickname,
		IsVip:        info.IsVip,
		TotalSpace:   info.TotalSpace,
		UsedSpace:    info.UsedSpace,
		FreeSpace:    info.FreeSpace,
	}, nil
}

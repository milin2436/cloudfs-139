package cloudfs

import (
	"context"
	"fmt"

	"github.com/milin2436/cloudfs-139/cloudfso/utils"
	"github.com/milin2436/cloudfs-139/drivers/yun139"
)

// diskQuotaGettable is the shape *Yun139 exposes for its raw quota report.
//
// Same pattern as userInfoGettable: cloudfso/driver exposes only
// TotalSpace / UsedSpace, so the per-drive-type breakdown is reachable only
// by type assertion. A mount that is not 139Yun simply does not offer it.
type diskQuotaGettable interface {
	GetDiskQuota(ctx context.Context) (*yun139.DiskQuotaDetail, error)
}

// Quota is 139's account quota in its native unit, MB (1024 KiB), plus the
// per-drive-type breakdown that the generic Space throws away.
//
// 139 has no per-directory quota, so there is no path dimension here: drive
// type is the finest split it reports. FreeDiskSizeMB is negative when the
// account is over quota, and Items may sum to more than DiskSizeMB, because
// 家庭云 draws on the same pool as 个人云 — the sum of Items is what the
// account has actually consumed.
type Quota struct {
	DiskSizeMB     int64       `json:"disk_size_mb"`
	FreeDiskSizeMB int64       `json:"free_disk_size_mb"`
	Items          []QuotaItem `json:"items"`
}

// QuotaItem is one drive type's contribution to the account's used space.
type QuotaItem struct {
	// DriveType is 1 个人云, 2 家庭云, 3 共享群, 4 圈子, 8 笔记, 9 群组.
	DriveType int    `json:"drive_type"`
	Name      string `json:"name"`
	UsedMB    int64  `json:"used_mb"`
}

// TotalBytes is the quota in bytes.
func (q *Quota) TotalBytes() int64 { return q.DiskSizeMB * utils.MB }

// FreeBytes is negative when the account is over quota.
func (q *Quota) FreeBytes() int64 { return q.FreeDiskSizeMB * utils.MB }

// UsedBytes is the account's consumption in bytes: total minus the possibly
// negative free amount, which is what keeps it correct over quota.
func (q *Quota) UsedBytes() int64 {
	return (q.DiskSizeMB - q.FreeDiskSizeMB) * utils.MB
}

// Quota returns 139Yun's account quota and per-drive-type usage, or to
// default_mount when mountPath is empty. Sizes are in MB.
func (c *Client) Quota(ctx context.Context, mountPath string) (*Quota, error) {
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

	q, ok := d.(diskQuotaGettable)
	if !ok {
		return nil, fmt.Errorf(
			"storage %q uses %s, which does not report a per-drive-type quota",
			mount, d.Config().Name)
	}
	detail, err := q.GetDiskQuota(ctx)
	if err != nil {
		return nil, fmt.Errorf("storage %q: %w", mount, err)
	}
	if detail == nil {
		return nil, fmt.Errorf("storage %q returned no quota", mount)
	}

	items := make([]QuotaItem, 0, len(detail.Data.QuotaList))
	for _, it := range detail.Data.QuotaList {
		items = append(items, QuotaItem{
			DriveType: it.DriveType,
			Name:      it.DriveName,
			UsedMB:    it.UsedSize,
		})
	}
	return &Quota{
		DiskSizeMB:     detail.Data.DiskSize,
		FreeDiskSizeMB: detail.Data.FreeDiskSize,
		Items:          items,
	}, nil
}

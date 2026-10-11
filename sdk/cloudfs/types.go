package cloudfs

import "time"

// File is the SDK's own view of an object on the cloud.
//
// cloudfso/model.Object carries no json tags, so serialising a driver object
// straight out would yield uppercase field names ({"Name":...,"Size":...}).
// File fixes the names to the ones documented in the README.
type File struct {
	ID      string    `json:"id,omitempty"`
	Name    string    `json:"name"`
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	Ctime   time.Time `json:"ctime,omitempty"`
	Path    string    `json:"path"`
	Type    string    `json:"type"`
}

// Space is the quota of one mount, in bytes.
type Space struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
	Free  int64 `json:"free"`
}

// UserInfo is the account bound to a mount. A driver leaves fields it cannot
// resolve at their zero value instead of reporting an error.
type UserInfo struct {
	Account      string `json:"account"`
	Username     string `json:"username"`
	UserDomainID string `json:"user_domain_id,omitempty"`
	Nickname     string `json:"nickname,omitempty"`
	IsVip        bool   `json:"is_vip"`
	TotalSpace   int64  `json:"total_space"`
	UsedSpace    int64  `json:"used_space"`
	FreeSpace    int64  `json:"free_space"`
}

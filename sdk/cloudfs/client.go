package cloudfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/glebarez/sqlite"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"github.com/milin2436/cloudfs-139/cloudfso/base"
	"github.com/milin2436/cloudfs-139/cloudfso/conf"
	"github.com/milin2436/cloudfs-139/cloudfso/db"
	"github.com/milin2436/cloudfs-139/cloudfso/driver"
	"github.com/milin2436/cloudfs-139/cloudfso/http_range"
	"github.com/milin2436/cloudfs-139/cloudfso/model"
	"github.com/milin2436/cloudfs-139/cloudfso/net"
	"github.com/milin2436/cloudfs-139/cloudfso/op"
	"github.com/milin2436/cloudfs-139/cloudfso/stream"
	"github.com/milin2436/cloudfs-139/cloudfso/utils"

	// Imports the 139Yun driver. The import registers it through
	// op.RegisterDriver; the named reference is also used by Space to turn a
	// quota refusal into an actionable error.
	yun139 "github.com/milin2436/cloudfs-139/drivers/yun139"
)

// sqliteDSN is an in-memory database that survives for the whole process.
// cache=shared makes the connections gorm pools out share one database
// instead of each getting its own private copy; busy_timeout absorbs the
// occasional concurrent write from storages initialised in parallel.
//
// The `mode=memory` query parameter is load-bearing. Only the exact string
// ":memory:" is special-cased by SQLite as an anonymous database — any other
// name is treated as a filename. So the tempting `file::memory:cloudfs?...`
// silently creates a real file literally named ":memory:cloudfs" in the
// process working directory: it leaks storage metadata (including the
// addition JSON that carries the live 139 Authorization token) to an
// arbitrary cwd, and because the file persists across invocations the second
// `cloudfs ls` in the same directory dies on a UNIQUE constraint for
// storages.mount_path instead of starting fresh. `mode=memory` plus
// cache=shared gives a named database that stays in memory.
// See TestSQLiteDSNIsInMemoryAndShared for the regression guard.
const sqliteDSN = "file:cloudfs?mode=memory&cache=shared&_pragma=busy_timeout(5000)"

// Client is the SDK entry point. It owns a validated config and lazily boots
// the driver framework on the first operation.
type Client struct {
	cfg      *Config
	initOnce sync.Once
	initErr  error
}

// NewClientFromFile loads the config at path ("" = ~/.cloudfs.json), boots the
// framework and initialises every configured storage.
func NewClientFromFile(ctx context.Context, path string) (*Client, error) {
	cfg, err := LoadConfigFromFile(path)
	if err != nil {
		return nil, err
	}
	c, err := NewClient(cfg)
	if err != nil {
		return nil, err
	}
	if err := c.ensureInit(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// NewClient builds a client from an in-memory config. The framework is booted
// on the first operation, or eagerly via Init.
func NewClient(cfg *Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Client{cfg: cfg}, nil
}

// Init boots the framework eagerly instead of waiting for the first operation.
func (c *Client) Init(ctx context.Context) error {
	return c.ensureInit(ctx)
}

// Config returns the validated config this client was built from.
func (c *Client) Config() *Config {
	return c.cfg
}

// Storages returns the configured mount paths.
func (c *Client) Storages() []string {
	return c.cfg.MountPaths()
}

// ResolveMountPath resolves an optional mount path: "" falls back to
// cfg.default_mount, then to the first configured storage. It is the same rule
// Space and UserInfo apply, exposed so a caller can print or target a mount.
func (c *Client) ResolveMountPath(mountPath string) (string, error) {
	return c.mountPath(mountPath)
}

func (c *Client) ensureInit(ctx context.Context) error {
	c.initOnce.Do(func() {
		c.initErr = c.onceInit(ctx)
	})
	return c.initErr
}

// onceInit runs the framework bootstrap in the documented order:
//
//	conf.Conf -> gorm.Open -> db.Init -> base.InitClient -> op.CreateStorage
//
// Driver registration itself happens through the blank import of
// drivers/yun139 above: its init() calls op.RegisterDriver.
func (c *Client) onceInit(ctx context.Context) error {
	if conf.Conf == nil {
		conf.Conf = &conf.Config{
			Database: conf.Database{Type: "sqlite3"},
			TempDir:  os.TempDir(),
		}
	}

	handle, err := gorm.Open(sqlite.Open(sqliteDSN), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	// db.Init sets the process-wide gorm handle and migrates the tables the
	// framework writes to. It log.Fatals on failure.
	db.Init(handle)
	base.InitClient()

	for _, sc := range c.cfg.Storages {
		mount := utils.FixAndCleanPath(sc.MountPath)
		addition, err := marshalValue(normalizeAddition(sc.Addition))
		if err != nil {
			return fmt.Errorf("storage %q: marshal addition: %w", mount, err)
		}
		id, err := op.CreateStorage(ctx, model.Storage{
			MountPath: mount,
			Driver:    NormalizeDriverName(sc.Driver),
			Addition:  string(addition),
		})
		if err != nil {
			return fmt.Errorf("storage %q (%s): %w", mount, sc.Driver, err)
		}
		log.WithFields(log.Fields{"id": id, "mount": mount, "driver": sc.Driver}).
			Debugf("storage created")
	}
	return nil
}

// normalizeAddition returns a copy of addition with the 139Yun driver default
// applied, leaving the caller's config untouched.
//
// The `default:"personal_new"` struct tag on yun139.Addition.Type is only a
// hint for OpenList's web form renderer — JSON unmarshalling does not apply
// it. A config file that omits "type" leaves Addition.Type empty, and the
// driver's Init then falls straight through its account-type switch to
// errs.NotImplement, which reaches the user as an opaque "not implement" that
// points at nothing. Filling the documented default here keeps a hand-written
// config working the way one created in the web UI does.
//
// A misspelled type is deliberately not papered over: only an empty or absent
// value is filled in, so a typo still fails loudly.
func normalizeAddition(addition JSON) JSON {
	out := make(JSON, len(addition))
	for k, v := range addition {
		out[k] = v
	}
	if s := strings.TrimSpace(fmt.Sprintf("%v", addition["Type"])); s == "" || s == "<nil>" {
		out["Type"] = "personal_new"
		log.Debugf("addition.type is empty, defaulting to %q", out["Type"])
	}
	return out
}

// ---------------------------------------------------------------------------
// path resolution
// ---------------------------------------------------------------------------

// resolvePath splits a virtual path into the mount it belongs to and the path
// relative to that mount. It uses the longest matching mount prefix so that
// /yun139 and /yun139/backup cannot both claim /yun139/backup/a.jpg.
func (c *Client) resolvePath(rawPath string) (mountPath, relPath string, err error) {
	abs := utils.FixAndCleanPath(rawPath)
	if abs == "/" {
		return "", "", errors.New("path \"/\" is the virtual root; list the mounts instead")
	}

	best := ""
	for _, sc := range c.cfg.Storages {
		mount := utils.FixAndCleanPath(sc.MountPath)
		if utils.IsSubPath(mount, abs) && len(mount) > len(best) {
			best = mount
		}
	}
	if best == "" {
		return "", "", fmt.Errorf(
			"path %q is not under any configured mount (%s)", rawPath, strings.Join(c.cfg.MountPaths(), ", "))
	}

	rel := strings.TrimPrefix(abs, best)
	return best, strings.TrimPrefix(rel, "/"), nil
}

// mountPath resolves an optional mount path: "" falls back to default_mount,
// then to the first configured storage.
//
// Membership is checked against the config, not op's live storage map, so
// resolution is deterministic and does not depend on the framework having been
// booted yet. driverForMount is the single call site that touches the map.
func (c *Client) mountPath(mountPath string) (string, error) {
	if strings.TrimSpace(mountPath) == "" {
		return c.defaultMount()
	}
	return c.validateMount(mountPath)
}

func (c *Client) defaultMount() (string, error) {
	if dm := c.cfg.DefaultMount; dm != "" && c.mountExists(dm) {
		return utils.FixAndCleanPath(dm), nil
	}
	if len(c.cfg.Storages) > 0 {
		return utils.FixAndCleanPath(c.cfg.Storages[0].MountPath), nil
	}
	return "", errors.New("no storage configured")
}

func (c *Client) validateMount(mountPath string) (string, error) {
	mount := utils.FixAndCleanPath(mountPath)
	if !c.mountExists(mount) {
		return "", fmt.Errorf(
			"storage %q not found (configured: %s)", mountPath, strings.Join(c.cfg.MountPaths(), ", "))
	}
	return mount, nil
}

func (c *Client) mountExists(mountPath string) bool {
	want := utils.FixAndCleanPath(mountPath)
	for _, s := range c.cfg.Storages {
		if utils.FixAndCleanPath(s.MountPath) == want {
			return true
		}
	}
	return false
}

// driverForMount looks a mount up in the framework's storage map.
func (c *Client) driverForMount(mountPath string) (driver.Driver, error) {
	d, err := op.GetStorageByMountPath(mountPath)
	if err != nil {
		return nil, fmt.Errorf("storage %q: %w", mountPath, err)
	}
	if d == nil {
		return nil, fmt.Errorf("storage %q not found", mountPath)
	}
	return d, nil
}

// sameMount guards Move/Copy, which cannot cross a mount boundary.
func (c *Client) sameMount(src, dst string) (mount, relSrc, relDst string, err error) {
	mount, relSrc, err = c.resolvePath(src)
	if err != nil {
		return "", "", "", fmt.Errorf("source: %w", err)
	}
	mountDst, relDst, err := c.resolvePath(dst)
	if err != nil {
		return "", "", "", fmt.Errorf("destination: %w", err)
	}
	if mountDst != mount {
		return "", "", "", fmt.Errorf(
			"move %q -> %q crosses mounts (%s -> %s), which is not supported", src, dst, mount, mountDst)
	}
	return mount, relSrc, relDst, nil
}

// ---------------------------------------------------------------------------
// object conversion
// ---------------------------------------------------------------------------

// toFile maps a driver object to the SDK File type. rel is the object's own
// path relative to the mount.
//
// rel must be supplied by the caller: neither op.List nor the drivers populate
// Object.Path on list results (only the synthetic root gets one), so falling
// back to o.GetPath() makes every entry report the bare mount path.
func toFile(mountPath, rel string, o model.Obj) File {
	if rel == "" {
		rel = o.GetPath()
	}
	return File{
		ID:      o.GetID(),
		Name:    o.GetName(),
		IsDir:   o.IsDir(),
		Size:    o.GetSize(),
		ModTime: o.ModTime(),
		Ctime:   o.CreateTime(),
		Path:    joinPath(mountPath, rel),
		Type:    objectType(o),
	}
}

func objectType(o model.Obj) string {
	if o.IsDir() {
		return "dir"
	}
	return "file"
}

func joinPath(mount, rel string) string {
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return mount
	}
	return strings.TrimRight(mount, "/") + "/" + rel
}

// childPath joins a directory relative to the mount with an entry name, for
// the objects op.List hands back — which carry no path of their own.
func childPath(dir, name string) string {
	dir = strings.Trim(dir, "/")
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// ---------------------------------------------------------------------------
// operations
// ---------------------------------------------------------------------------

// List returns the children of a directory. "/" lists the configured mounts.
func (c *Client) List(ctx context.Context, remotePath string) ([]File, error) {
	if err := c.ensureInit(ctx); err != nil {
		return nil, err
	}

	if utils.FixAndCleanPath(remotePath) == "/" {
		files := make([]File, 0, len(c.cfg.Storages))
		for _, sc := range c.cfg.Storages {
			mount := utils.FixAndCleanPath(sc.MountPath)
			files = append(files, File{
				Name:  strings.TrimPrefix(mount, "/"),
				IsDir: true,
				Path:  mount,
				Type:  "dir",
			})
		}
		return files, nil
	}

	mount, rel, err := c.resolvePath(remotePath)
	if err != nil {
		return nil, err
	}
	d, err := c.driverForMount(mount)
	if err != nil {
		return nil, err
	}

	objs, err := op.List(ctx, d, rel, model.ListArgs{})
	if err != nil {
		return nil, err
	}
	files := make([]File, 0, len(objs))
	for _, o := range objs {
		files = append(files, toFile(mount, childPath(rel, o.GetName()), o))
	}
	return files, nil
}

// Stat returns the metadata of one object.
func (c *Client) Stat(ctx context.Context, remotePath string) (*File, error) {
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
	obj, err := op.Get(ctx, d, rel, true)
	if err != nil {
		return nil, err
	}
	f := toFile(mount, rel, obj)
	return &f, nil
}

// Download writes one file to dest and returns the number of bytes written.
//
// Drivers may answer a Link request with either a plain URL or with a
// RangeReader; both shapes are handled.
func (c *Client) Download(ctx context.Context, remotePath string, dest io.Writer) (int64, error) {
	if err := c.ensureInit(ctx); err != nil {
		return 0, err
	}
	mount, rel, err := c.resolvePath(remotePath)
	if err != nil {
		return 0, err
	}
	d, err := c.driverForMount(mount)
	if err != nil {
		return 0, err
	}

	link, obj, err := op.Link(ctx, d, rel, model.LinkArgs{})
	if err != nil {
		return 0, err
	}
	defer link.Close()

	if link.URL != "" {
		resp, err := net.RequestHttp(ctx, http.MethodGet, link.Header, link.URL)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		return io.Copy(dest, resp.Body)
	}

	rr, err := stream.GetRangeReaderFromLink(obj.GetSize(), link)
	if err != nil {
		return 0, err
	}
	rc, err := rr.RangeRead(ctx, http_range.Range{Length: -1})
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	return io.Copy(dest, rc)
}

// Get is an alias for Download.
func (c *Client) Get(ctx context.Context, remotePath string, dest io.Writer) (int64, error) {
	return c.Download(ctx, remotePath, dest)
}

// Upload writes a local file into a remote directory. The file's basename is
// used as the remote name. up may be nil.
func (c *Client) Upload(ctx context.Context, remoteDir, localPath string, up driver.UpdateProgress) error {
	if err := c.ensureInit(ctx); err != nil {
		return err
	}
	if up == nil {
		up = func(float64) {}
	}

	mount, rel, err := c.resolvePath(remoteDir)
	if err != nil {
		return err
	}
	d, err := c.driverForMount(mount)
	if err != nil {
		return err
	}

	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open %q: %w", localPath, err)
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %q: %w", localPath, err)
	}
	if st.IsDir() {
		return fmt.Errorf("%q is a directory; upload a single file", localPath)
	}

	// FileStream.size is unexported, so the size has to arrive through the
	// embedded Obj. FileStream.GetSize then falls back to Obj.GetSize and
	// op.Put reads the body straight off io.Reader.
	file := &stream.FileStream{
		Ctx:      ctx,
		Obj:      &model.Object{Name: st.Name(), Size: st.Size(), Modified: st.ModTime(), Ctime: st.ModTime()},
		Reader:   f,
		Mimetype: utils.GetMimeType(st.Name()),
	}
	return op.Put(ctx, d, rel, file, up)
}

// Put is an alias for Upload.
func (c *Client) Put(ctx context.Context, remoteDir, localPath string, up driver.UpdateProgress) error {
	return c.Upload(ctx, remoteDir, localPath, up)
}

// PutURL asks the driver to fetch url and store it into remoteDir as remoteName.
func (c *Client) PutURL(ctx context.Context, remoteDir, remoteName, url string) error {
	if err := c.ensureInit(ctx); err != nil {
		return err
	}
	mount, rel, err := c.resolvePath(remoteDir)
	if err != nil {
		return err
	}
	d, err := c.driverForMount(mount)
	if err != nil {
		return err
	}
	return op.PutURL(ctx, d, rel, remoteName, url)
}

// MakeDir creates a directory. Missing parents are created by the driver.
func (c *Client) MakeDir(ctx context.Context, remotePath string) error {
	if err := c.ensureInit(ctx); err != nil {
		return err
	}
	mount, rel, err := c.resolvePath(remotePath)
	if err != nil {
		return err
	}
	d, err := c.driverForMount(mount)
	if err != nil {
		return err
	}
	return op.MakeDir(ctx, d, rel)
}

// Remove deletes a file or directory.
func (c *Client) Remove(ctx context.Context, remotePath string) error {
	if err := c.ensureInit(ctx); err != nil {
		return err
	}
	mount, rel, err := c.resolvePath(remotePath)
	if err != nil {
		return err
	}
	d, err := c.driverForMount(mount)
	if err != nil {
		return err
	}
	return op.Remove(ctx, d, rel)
}

// Move relocates an object into destDir. Both paths must be under the same
// mount.
func (c *Client) Move(ctx context.Context, remotePath, destDir string) error {
	if err := c.ensureInit(ctx); err != nil {
		return err
	}
	mount, relSrc, relDst, err := c.sameMount(remotePath, destDir)
	if err != nil {
		return err
	}
	d, err := c.driverForMount(mount)
	if err != nil {
		return err
	}
	return op.Move(ctx, d, relSrc, relDst)
}

// Copy duplicates an object into destDir. Both paths must be under the same
// mount.
func (c *Client) Copy(ctx context.Context, remotePath, destDir string) error {
	if err := c.ensureInit(ctx); err != nil {
		return err
	}
	mount, relSrc, relDst, err := c.sameMount(remotePath, destDir)
	if err != nil {
		return err
	}
	d, err := c.driverForMount(mount)
	if err != nil {
		return err
	}
	return op.Copy(ctx, d, relSrc, relDst)
}

// Rename changes only the last segment of remotePath.
func (c *Client) Rename(ctx context.Context, remotePath, newName string) error {
	if err := c.ensureInit(ctx); err != nil {
		return err
	}
	mount, rel, err := c.resolvePath(remotePath)
	if err != nil {
		return err
	}
	d, err := c.driverForMount(mount)
	if err != nil {
		return err
	}
	return op.Rename(ctx, d, rel, newName)
}

// Space returns the quota of a mount, or of default_mount when mountPath is
// empty.
func (c *Client) Space(ctx context.Context, mountPath string) (*Space, error) {
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
	details, err := op.GetStorageDetails(ctx, d)
	if err != nil {
		// 139Yun answers the quota query only when user_domain_id is set, and
		// op turns its refusal into a bare "not implement" that tells the user
		// nothing about what is actually missing.
		if y, ok := d.(*yun139.Yun139); ok && y.UserDomainID == "" {
			return nil, fmt.Errorf(
				"storage %q: 139Yun reports no quota without user_domain_id in addition "+
					"(the ud_id value from your yun.139.com cookie); %w", mount, err)
		}
		return nil, err
	}
	return &Space{
		Total: details.TotalSpace,
		Used:  details.UsedSpace,
		Free:  details.FreeSpace(),
	}, nil
}

// Other forwards to a driver's Other method for driver-specific extensions,
// e.g. yun139's "video_preview".
func (c *Client) Other(ctx context.Context, remotePath, method string, data any) (any, error) {
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
	return op.Other(ctx, d, model.FsOtherArgs{Path: rel, Method: method, Data: data})
}

// ---------------------------------------------------------------------------
// debugging helpers
// ---------------------------------------------------------------------------

// EnableHTTPCapture arms hooks on the shared resty clients so that subsequent
// driver traffic is recorded. Call DisableHTTPCapture afterwards.
func EnableHTTPCapture() {
	base.EnableHTTPCapture()
}

// DisableHTTPCapture stops recording but keeps the recorded traffic.
func DisableHTTPCapture() {
	base.DisableHTTPCapture()
}

// FormatHTTPCaptures renders the recorded traffic for a terminal, or an empty
// string if nothing was recorded.
func FormatHTTPCaptures() string {
	captures := base.GetHTTPCaptures()
	if len(captures) == 0 {
		return ""
	}
	return base.FormatCaptures(captures)
}

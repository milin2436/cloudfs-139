package cloudfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultConfigFileName is looked up in the home directory when no --config
// path is given. It is ignored by VCS on purpose: it holds live tokens.
const DefaultConfigFileName = ".cloudfs.json"

// DefaultSampleMount is the mount path used by SampleConfig and `cloudfs init`.
const DefaultSampleMount = "/yun139"

// DriverYun139 is the name the 139 driver registers under in cloudfso/op.
const DriverYun139 = "139Yun"

// DriverYun139Alias is the spelling used in the README; accepted for compat.
const DriverYun139Alias = "yun139"

// JSON is a JSON object. It round-trips unknown keys losslessly, which matters
// because an addition block is forwarded verbatim to a driver's Addition
// struct, and this SDK does not want to own a copy of every driver's fields.
type JSON map[string]any

func (j JSON) MarshalJSON() ([]byte, error) {
	// The explicit conversion is what breaks the recursion; see json.go.
	return marshalValue(map[string]any(j))
}

func (j *JSON) UnmarshalJSON(data []byte) error {
	var v map[string]any
	if err := unmarshalValue(data, &v); err != nil {
		return err
	}
	*j = v
	return nil
}

// StorageCfg is one entry of the storages array.
type StorageCfg struct {
	MountPath string `json:"mount_path"`
	Driver    string `json:"driver"`
	Addition  JSON   `json:"addition"`
}

// Config is the on-disk config layout.
type Config struct {
	Storages     []StorageCfg `json:"storages"`
	DefaultMount string       `json:"default_mount,omitempty"`
}

// Validate catches config mistakes that would otherwise surface later as
// obscure driver errors.
func (c *Config) Validate() error {
	if c == nil {
		return errors.New("config is nil")
	}
	if len(c.Storages) == 0 {
		return errors.New("at least one storage entry is required")
	}
	mounts := make(map[string]struct{}, len(c.Storages))
	for i, s := range c.Storages {
		mount := strings.TrimSpace(s.MountPath)
		if mount == "" {
			return fmt.Errorf("storages[%d]: mount_path is required", i)
		}
		if !strings.HasPrefix(mount, "/") {
			return fmt.Errorf("storages[%d]: mount_path %q must start with /", i, mount)
		}
		if _, dup := mounts[mount]; dup {
			return fmt.Errorf("storages[%d]: duplicate mount_path %q", i, mount)
		}
		mounts[mount] = struct{}{}
		if strings.TrimSpace(s.Driver) == "" {
			return fmt.Errorf("storages[%d] %q: driver is required", i, mount)
		}
	}
	return nil
}

// Marshal serialises the config for writing to disk.
func (c *Config) Marshal() ([]byte, error) {
	return marshalValue(c)
}

// ResolveConfigPath expands an empty path to ~/.cloudfs.json.
func ResolveConfigPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve default config path: %w", err)
		}
		return filepath.Join(home, DefaultConfigFileName), nil
	}
	return path, nil
}

// LoadConfigFromFile reads, parses and validates a config file. An empty path
// resolves to ~/.cloudfs.json.
func LoadConfigFromFile(path string) (*Config, error) {
	resolved, err := ResolveConfigPath(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", resolved, err)
	}
	cfg := new(Config)
	if err := unmarshalValue(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", resolved, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config %q: %w", resolved, err)
	}
	return cfg, nil
}

// SampleConfig returns a writable starter config for one 139 account. The
// placeholders are empty on purpose — a token must be pasted in by hand.
func SampleConfig(driverName string) *Config {
	if strings.TrimSpace(driverName) == "" {
		driverName = DriverYun139
	}
	return &Config{
		Storages: []StorageCfg{{
			MountPath: DefaultSampleMount,
			Driver:    NormalizeDriverName(driverName),
			Addition: JSON{
				"Authorization": "",
				"Username":      "",
				"Password":      "",
				"RootFolderID":  "",
				"Type":          "personal_new",
			},
		}},
		DefaultMount: DefaultSampleMount,
	}
}

// NormalizeDriverName accepts the README spelling "yun139" as well as the name
// the driver actually registers under, "139Yun".
func NormalizeDriverName(name string) string {
	if strings.EqualFold(name, DriverYun139Alias) {
		return DriverYun139
	}
	return name
}

// MountPaths returns the configured mount paths in config order.
func (c *Config) MountPaths() []string {
	out := make([]string, 0, len(c.Storages))
	for _, s := range c.Storages {
		out = append(out, s.MountPath)
	}
	return out
}

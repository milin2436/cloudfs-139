package cloudfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/milin2436/cloudfs-139/cloudfso/model"
)

func TestConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     *Config
		wantErr string
	}{
		{"nil", nil, "config is nil"},
		{"no storages", &Config{}, "at least one storage"},
		{"empty mount", &Config{Storages: []StorageCfg{{Driver: "139Yun"}}}, "mount_path is required"},
		{"bad mount prefix", &Config{Storages: []StorageCfg{{MountPath: "yun139", Driver: "139Yun"}}}, "must start with /"},
		{"duplicate mount", &Config{Storages: []StorageCfg{
			{MountPath: "/yun139", Driver: "139Yun"},
			{MountPath: "/yun139", Driver: "139Yun"}}}, "duplicate mount_path"},
		{"empty driver", &Config{Storages: []StorageCfg{{MountPath: "/yun139"}}}, "driver is required"},
		{
			name:    "ok",
			cfg:     &Config{Storages: []StorageCfg{{MountPath: "/yun139", Driver: "139Yun"}}, DefaultMount: "/yun139"},
			wantErr: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// Guard for the marshalling trap documented in DEV.md §4.1: JSON.MarshalJSON
// must not call json.Marshal(j) directly or it recurses until the stack
// overflows. Nested JSON values make the failure immediate if it regresses.
func TestJSONMarshalNoRecursion(t *testing.T) {
	j := JSON{
		"Authorization": "tok",
		"Type":          "personal_new",
		"Nested":        JSON{"inner": JSON{"deep": "value"}},
	}
	data, err := marshalValue(j)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back map[string]any
	if err := unmarshalValue(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back["Authorization"] != "tok" {
		t.Fatalf("Authorization = %v", back["Authorization"])
	}
	nested := back["Nested"].(map[string]any)
	if nested["inner"].(map[string]any)["deep"] != "value" {
		t.Fatalf("Nested.inner.deep = %v", nested["inner"])
	}
}

func TestConfigRoundTrip(t *testing.T) {
	cfg := &Config{
		Storages: []StorageCfg{{
			MountPath: "/yun139",
			Driver:    "139Yun",
			Addition: JSON{
				"Authorization": "tok-1",
				"Username":      "13800138000",
				"RootFolderID":  "",
				"Type":          "personal_new",
				"CustomUnknown": "preserved",
			},
		}},
		DefaultMount: "/yun139",
	}
	data, err := cfg.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Config
	if err := unmarshalValue(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Storages[0].MountPath != "/yun139" || back.Storages[0].Driver != "139Yun" {
		t.Fatalf("got %+v", back.Storages[0])
	}
	add := back.Storages[0].Addition
	if add["Authorization"] != "tok-1" || add["CustomUnknown"] != "preserved" {
		t.Fatalf("addition round-trip failed: %+v", add)
	}
}

func TestSampleConfigIsLoadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cloudfs.json")
	data, err := SampleConfig("").Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfigFromFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Storages[0].Driver != DriverYun139 {
		t.Fatalf("driver = %q, want %q", cfg.Storages[0].Driver, DriverYun139)
	}
	if cfg.DefaultMount != DefaultSampleMount {
		t.Fatalf("default_mount = %q", cfg.DefaultMount)
	}
}

func TestResolveConfigPath(t *testing.T) {
	explicit, err := ResolveConfigPath("/tmp/x/cloudfs.json")
	if err != nil || explicit != "/tmp/x/cloudfs.json" {
		t.Fatalf("explicit path = %q, %v", explicit, err)
	}

	home, err := ResolveConfigPath("")
	if err != nil {
		t.Fatalf("default path: %v", err)
	}
	if !strings.HasSuffix(home, DefaultConfigFileName) {
		t.Fatalf("default path %q does not end with %q", home, DefaultConfigFileName)
	}
	if !strings.HasSuffix(home, filepath.Join("/", DefaultConfigFileName)) {
		t.Fatalf("default path %q is not in the home directory", home)
	}
}

func TestNormalizeDriverName(t *testing.T) {
	cases := map[string]string{
		"yun139":       DriverYun139,
		"YUN139":       DriverYun139,
		"139Yun":       "139Yun",
		"someOtherDrv": "someOtherDrv",
	}
	for in, want := range cases {
		if got := NormalizeDriverName(in); got != want {
			t.Fatalf("NormalizeDriverName(%q) = %q, want %q", in, got, want)
		}
	}
}

// resolvePath must pick the longest matching mount so that /yun139/backup/x
// cannot be claimed by the shorter /yun139 mount.
func TestResolvePath(t *testing.T) {
	c, err := NewClient(&Config{
		Storages: []StorageCfg{
			{MountPath: "/yun139", Driver: "139Yun"},
			{MountPath: "/yun139/backup", Driver: "139Yun"},
			{MountPath: "/family", Driver: "139Yun"},
		},
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ok := []struct{ in, mount, rel string }{
		{"/yun139", "/yun139", ""},
		{"/yun139/photos/a.jpg", "/yun139", "photos/a.jpg"},
		{"/yun139/backup/x.tar", "/yun139/backup", "x.tar"},
		{"/yun139/backup", "/yun139/backup", ""},
		{"yun139/photos/a.jpg", "/yun139", "photos/a.jpg"},
		{"/family/相册", "/family", "相册"},
		// .. is normalized before matching, so this resolves to /family.
		{"/yun139/../family", "/family", ""},
	}
	for _, tc := range ok {
		mount, rel, err := c.resolvePath(tc.in)
		if err != nil {
			t.Fatalf("resolvePath(%q): %v", tc.in, err)
		}
		if mount != tc.mount || rel != tc.rel {
			t.Fatalf("resolvePath(%q) = (%q, %q), want (%q, %q)", tc.in, mount, rel, tc.mount, tc.rel)
		}
	}

	bad := []string{"/", "/yun139X/a", "/nope"}
	for _, in := range bad {
		if _, _, err := c.resolvePath(in); err == nil {
			t.Fatalf("resolvePath(%q) should have failed", in)
		}
	}
}

func TestResolveMountPathFallsBack(t *testing.T) {
	c, err := NewClient(&Config{
		Storages: []StorageCfg{
			{MountPath: "/yun139", Driver: "139Yun"},
			{MountPath: "/family", Driver: "139Yun"},
		},
		DefaultMount: "/family",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if got, err := c.mountPath(""); err != nil || got != "/family" {
		t.Fatalf("mountPath(\"\") = (%q, %v), want (/family, nil)", got, err)
	}

	// An explicitly named configured mount resolves regardless of whether the
	// framework has been booted: resolution reads the config, not the runtime
	// storage map.
	if got, err := c.mountPath("/family"); err != nil || got != "/family" {
		t.Fatalf("mountPath(/family) = (%q, %v), want (/family, nil)", got, err)
	}
	// A prefix that is not itself a mount is rejected rather than matched.
	if _, err := c.mountPath("/yun139/backup"); err == nil {
		t.Fatal("mountPath(/yun139/backup) should fail, it is not a configured mount")
	}
	if _, err := c.mountPath("/nope"); err == nil {
		t.Fatal("mountPath(/nope) should fail")
	}
}

func TestDefaultMountFallsBackToFirstStorage(t *testing.T) {
	c, err := NewClient(&Config{
		Storages: []StorageCfg{
			{MountPath: "/yun139", Driver: "139Yun"},
			{MountPath: "/family", Driver: "139Yun"},
		},
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if got, err := c.mountPath(""); err != nil || got != "/yun139" {
		t.Fatalf("mountPath(\"\") = (%q, %v), want (/yun139, nil)", got, err)
	}

	// A default_mount that is not a configured mount is ignored.
	c2, err := NewClient(&Config{
		Storages:     []StorageCfg{{MountPath: "/yun139", Driver: "139Yun"}},
		DefaultMount: "/unknown",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if got, err := c2.mountPath(""); err != nil || got != "/yun139" {
		t.Fatalf("mountPath(\"\") = (%q, %v), want (/yun139, nil)", got, err)
	}
}

func TestMountPaths(t *testing.T) {
	got := (&Config{Storages: []StorageCfg{{MountPath: "/a"}, {MountPath: "/b"}}}).MountPaths()
	if len(got) != 2 || got[0] != "/a" || got[1] != "/b" {
		t.Fatalf("MountPaths = %v", got)
	}
}

// normalizeAddition must fill the documented personal_new default when type is
// absent or blank, but must not hide a typo — an unknown value would otherwise
// surface as an opaque "not implement" from the driver's Init.
func TestNormalizeAddition(t *testing.T) {
	cases := []struct {
		name  string
		add   JSON
		want  string
		fills bool
	}{
		{"absent", JSON{"Authorization": "tok"}, "personal_new", true},
		{"nil value", JSON{"Authorization": "tok", "Type": nil}, "personal_new", true},
		{"empty", JSON{"Type": ""}, "personal_new", true},
		{"whitespace", JSON{"Type": "   "}, "personal_new", true},
		{"explicit", JSON{"Type": "personal_new"}, "personal_new", false},
		{"other type", JSON{"Type": "family"}, "family", false},
		{"typo is not fixed", JSON{"Type": "personla_new"}, "personla_new", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Snapshot the caller's map before, to prove it is not rewritten.
			orig, origHas := tc.add["Type"]
			got := normalizeAddition(tc.add)

			if s, ok := got["Type"].(string); !ok || s != tc.want {
				t.Fatalf("Type = %v, want %q", got["Type"], tc.want)
			}
			if got["Authorization"] != tc.add["Authorization"] {
				t.Fatalf("Authorization not preserved: %v", got["Authorization"])
			}
			if tc.fills && !origHas {
				// Default was applied and the input had no key at all, so the
				// key still must not have been added in place.
				if _, now := tc.add["Type"]; now {
					t.Fatalf("caller's map was mutated: %v", tc.add)
				}
			}
			if newType, newHas := tc.add["Type"]; newHas != origHas ||
				fmt.Sprintf("%v", newType) != fmt.Sprintf("%v", orig) {
				t.Fatalf("caller's map was mutated: was %v, now %v", orig, newType)
			}
		})
	}
}

// toFile must assemble the full path from the mount and the directory an
// object was listed from. Neither op.List nor the 139Yun driver sets
// Object.Path on list results, so relying on o.GetPath() collapsed every entry
// in the output to the bare mount path.
func TestToFilePaths(t *testing.T) {
	obj := &model.Object{Name: "x.txt", ID: "f1"}
	cases := []struct{ mount, rel, want string }{
		{"/yun139", "", "/yun139/x.txt"},
		{"/yun139", "genxr/shot", "/yun139/genxr/shot/x.txt"},
		{"/yun139/backup", "sub", "/yun139/backup/sub/x.txt"},
		{"/yun139", "/", "/yun139/x.txt"},
		{"/yun139", "/genxr/shot/", "/yun139/genxr/shot/x.txt"},
	}
	for _, tc := range cases {
		got := toFile(tc.mount, childPath(tc.rel, obj.GetName()), obj)
		if got.Path != tc.want {
			t.Fatalf("mount=%q rel=%q: path = %q, want %q", tc.mount, tc.rel, got.Path, tc.want)
		}
		if got.Name != "x.txt" || got.ID != "f1" {
			t.Fatalf("name/id not carried over: %+v", got)
		}
	}

	// A driver that does fill Path is still honoured when no rel is known.
	withPath := &model.Object{Name: "y", Path: "/driver/rel"}
	if got := toFile("/yun139", "", withPath); got.Path != "/yun139/driver/rel" {
		t.Fatalf("fallback path = %q, want /yun139/driver/rel", got.Path)
	}
}

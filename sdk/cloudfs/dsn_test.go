package cloudfs

import (
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// filesInDir returns the regular-file names in dir, sorted.
func filesInDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// TestSQLiteDSNIsInMemoryAndShared guards the sqliteDSN choice.
//
// Two regressions are locked in here:
//
//  1. The DSN must not create a file. SQLite only special-cases the exact
//     string ":memory:"; any other name after the "file:" prefix is treated
//     as a filename. So `file::memory:cloudfs?cache=shared` — which reads as
//     in-memory to a human — actually wrote a real ":memory:cloudfs" file into
//     the process working directory. That both leaked storage metadata (the
//     addition JSON carries the live 139 Authorization token) to an arbitrary
//     cwd, and persisted state across CLI invocations so a second run in the
//     same directory failed with "UNIQUE constraint failed:
//     storages.mount_path" instead of booting cleanly.
//
//  2. The database must be shared across the connection pools gorm opens. A
//     bare ":memory:" DSN gives every pooled connection its own private
//     database, so a table migrated by one pool is "no such table" to another.
//     `mode=memory&cache=shared` is what makes a named database genuinely
//     in-memory and shared.
func TestSQLiteDSNIsInMemoryAndShared(t *testing.T) {
	before := filesInDir(t, ".")

	gdb, err := gorm.Open(sqlite.Open(sqliteDSN), &gorm.Config{})
	if err != nil {
		t.Fatalf("open %q: %v", sqliteDSN, err)
	}
	if err := gdb.Exec("CREATE TABLE cloudfs_probe (k text PRIMARY KEY)").Error; err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := gdb.Exec("INSERT OR IGNORE INTO cloudfs_probe VALUES ('row-1')").Error; err != nil {
		t.Fatalf("insert: %v", err)
	}

	// A second pool over the same DSN must see the first pool's data.
	var n int64
	gdb2, err := gorm.Open(sqlite.Open(sqliteDSN), &gorm.Config{})
	if err != nil {
		t.Fatalf("reopen %q: %v", sqliteDSN, err)
	}
	if err := gdb2.Raw("SELECT COUNT(*) FROM cloudfs_probe").Scan(&n).Error; err != nil {
		t.Fatalf("count (pool must be shared, DSN=%q): %v", sqliteDSN, err)
	}
	if n != 1 {
		t.Fatalf("second pool sees %d row(s), want 1: %q is not a shared in-memory DB", n, sqliteDSN)
	}

	if got := filesInDir(t, "."); !reflect.DeepEqual(got, before) {
		t.Fatalf("DSN %q created files in the working directory: added %v",
			sqliteDSN, subtractList(before, got))
	}
}

// subtractList returns the entries in got that are not in want, for use in
// failure messages.
func subtractList(want, got []string) []string {
	set := make(map[string]bool, len(want))
	for _, w := range want {
		set[w] = true
	}
	var out []string
	for _, g := range got {
		if !set[g] {
			out = append(out, g)
		}
	}
	return out
}

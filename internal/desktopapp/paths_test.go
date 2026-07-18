package desktopapp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveDataPaths_BothExplicit(t *testing.T) {
	cfg, db, err := resolveDataPaths("/tmp/a.yaml", "/tmp/b.db")
	if err != nil {
		t.Fatalf("resolveDataPaths: %v", err)
	}
	if cfg != "/tmp/a.yaml" || db != "/tmp/b.db" {
		t.Errorf("got (%q, %q), want the explicit values untouched", cfg, db)
	}
}

func TestResolveDataPaths_DefaultsToUserDataDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg, db, err := resolveDataPaths("", "")
	if err != nil {
		t.Fatalf("resolveDataPaths: %v", err)
	}
	wantCfg := filepath.Join(home, ".voxeltoad", "desktop.yaml")
	wantDB := filepath.Join(home, ".voxeltoad", "desktop.db")
	if cfg != wantCfg || db != wantDB {
		t.Errorf("got (%q, %q), want (%q, %q)", cfg, db, wantCfg, wantDB)
	}
	if info, err := os.Stat(filepath.Join(home, ".voxeltoad")); err != nil || !info.IsDir() {
		t.Errorf("data dir not created: %v", err)
	}
}

func TestResolveDataPaths_MixedExplicitAndDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg, db, err := resolveDataPaths("/tmp/mine.yaml", "")
	if err != nil {
		t.Fatalf("resolveDataPaths: %v", err)
	}
	if cfg != "/tmp/mine.yaml" {
		t.Errorf("cfg = %q, want the explicit value", cfg)
	}
	if want := filepath.Join(home, ".voxeltoad", "desktop.db"); db != want {
		t.Errorf("db = %q, want %q", db, want)
	}
}

// A legacy ./desktop.yaml in the cwd moves into the default location when the
// user hasn't chosen a config path and the target doesn't exist yet.
func TestMigrateLegacyData_MovesCwdFiles(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	dstDir := t.TempDir()

	legacyYAML := filepath.Join(work, "desktop.yaml")
	legacyDB := filepath.Join(work, "desktop.db")
	legacyWAL := filepath.Join(work, "desktop.db-wal")
	for _, f := range []string{legacyYAML, legacyDB, legacyWAL} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed legacy file: %v", err)
		}
	}
	// -shm may legitimately be absent; migration must not require it.

	cfgDst := filepath.Join(dstDir, "desktop.yaml")
	dbDst := filepath.Join(dstDir, "desktop.db")
	migrateLegacyData(cfgDst, dbDst, false, false)

	for _, f := range []string{cfgDst, dbDst, dbDst + "-wal"} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("expected migrated file %s: %v", f, err)
		}
	}
	for _, f := range []string{legacyYAML, legacyDB, legacyWAL} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("legacy file %s should be gone after move", f)
		}
	}
}

// An existing target is never overwritten by migration.
func TestMigrateLegacyData_NeverOverwritesTarget(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	dstDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(work, "desktop.yaml"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgDst := filepath.Join(dstDir, "desktop.yaml")
	if err := os.WriteFile(cfgDst, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	migrateLegacyData(cfgDst, filepath.Join(dstDir, "desktop.db"), false, false)

	b, err := os.ReadFile(cfgDst)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "new" {
		t.Errorf("target overwritten: %q, want %q", b, "new")
	}
	if _, err := os.Stat(filepath.Join(work, "desktop.yaml")); err != nil {
		t.Errorf("legacy file should remain when target exists: %v", err)
	}
}

// Explicitly chosen paths opt out of migration entirely.
func TestMigrateLegacyData_ExplicitPathsSkipped(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	dstDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(work, "desktop.yaml"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "desktop.db"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	migrateLegacyData(filepath.Join(dstDir, "c.yaml"), filepath.Join(dstDir, "d.db"), true, true)

	if _, err := os.Stat(filepath.Join(dstDir, "c.yaml")); !os.IsNotExist(err) {
		t.Error("explicit config path must not receive migrated content")
	}
	if _, err := os.Stat(filepath.Join(work, "desktop.yaml")); err != nil {
		t.Error("legacy file should stay put when paths are explicit")
	}
}

// Running from inside the data dir itself must not self-move or error.
func TestMigrateLegacyData_SameLocationNoop(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := filepath.Join(dir, "desktop.yaml")
	if err := os.WriteFile(cfg, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	migrateLegacyData(cfg, filepath.Join(dir, "desktop.db"), false, false)
	b, err := os.ReadFile(cfg)
	if err != nil || string(b) != "x" {
		t.Errorf("same-location file disturbed: %v %q", err, b)
	}
}

func TestMoveFile_Rename(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a")
	dst := filepath.Join(dir, "b")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := moveFile(src, dst); err != nil {
		t.Fatalf("moveFile: %v", err)
	}
	b, err := os.ReadFile(dst)
	if err != nil || string(b) != "hello" {
		t.Fatalf("dst content: %v %q", err, b)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("src should be gone after move")
	}
}

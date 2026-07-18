// Data-file locations for the desktop gateway (config YAML + SQLite DB).
//
// Historically both files defaulted to the process working directory
// (./desktop.yaml, ./desktop.db). That is fine for `make desktop-web-dev`
// (scripts pass explicit paths into a temp WORKDIR) but broken for the
// double-clicked .app, whose cwd is whatever Finder/LaunchServices picks —
// each launch could create a fresh empty database in a different directory.
// The default is now ~/.voxeltoad/, with a one-time migration that moves
// cwd-era files over. Explicit -config/-db flags or DESKTOP_CONFIG/DESKTOP_DB
// env vars always win and never trigger migration.
package desktopapp

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

// defaultDataDirName is the hidden directory under the user's home that holds
// the desktop gateway's state files.
const defaultDataDirName = ".voxeltoad"

// resolveDataPaths turns the raw -config/-db flag values (empty = not given)
// into absolute file locations. Unspecified paths land in
// ~/.voxeltoad/desktop.yaml|desktop.db; the directory is created if needed.
func resolveDataPaths(cfgFlag, dbFlag string) (cfgPath, dbPath string, err error) {
	cfgPath, dbPath = cfgFlag, dbFlag
	if cfgPath != "" && dbPath != "" {
		return cfgPath, dbPath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("locate home directory for default data dir: %w", err)
	}
	dir := filepath.Join(home, defaultDataDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("create data dir %s: %w", dir, err)
	}
	if cfgPath == "" {
		cfgPath = filepath.Join(dir, "desktop.yaml")
	}
	if dbPath == "" {
		dbPath = filepath.Join(dir, "desktop.db")
	}
	return cfgPath, dbPath, nil
}

// migrateLegacyData moves cwd-era data files into the resolved default
// locations. A file is migrated only when (a) its path was NOT explicitly set
// by the user, (b) the new location does not exist yet, and (c) the legacy
// cwd file does. The SQLite WAL sidecars (-wal/-shm) move with the DB so no
// un-checkpointed writes are stranded. Migrations are logged; a file that
// cannot be moved is reported but does not abort startup (the fresh default
// will simply be seeded instead).
func migrateLegacyData(cfgPath, dbPath string, cfgExplicit, dbExplicit bool) {
	if !cfgExplicit {
		migrateLegacyFile("desktop.yaml", cfgPath)
	}
	if !dbExplicit {
		migrateLegacyFile("desktop.db", dbPath)
		migrateLegacyFile("desktop.db-wal", dbPath+"-wal")
		migrateLegacyFile("desktop.db-shm", dbPath+"-shm")
	}
}

// migrateLegacyFile moves legacyName (relative to the cwd) to dst when dst is
// absent and the legacy file exists.
func migrateLegacyFile(legacyName, dst string) {
	src, err := filepath.Abs(legacyName)
	if err != nil {
		return
	}
	absDst, err := filepath.Abs(dst)
	if err != nil || src == absDst {
		return // already at the target location (e.g. cwd IS the data dir)
	}
	if _, err := os.Stat(dst); err == nil {
		return // target exists — never overwrite user data
	}
	info, err := os.Stat(src)
	if err != nil || info.IsDir() {
		return // nothing to migrate
	}
	if err := os.MkdirAll(filepath.Dir(absDst), 0o755); err != nil {
		log.Printf("data migration: mkdir for %s failed: %v", absDst, err)
		return
	}
	if err := moveFile(src, absDst); err != nil {
		log.Printf("data migration: move %s -> %s failed: %v (a fresh file will be created instead)", src, absDst, err)
		return
	}
	log.Printf("data migration: moved %s -> %s", src, absDst)
}

// moveFile renames src to dst, falling back to copy+remove when the rename
// crosses filesystem boundaries (os.Rename fails with EXDEV).
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	return os.Remove(src)
}

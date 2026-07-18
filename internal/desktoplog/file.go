package desktoplog

import (
	"fmt"
	"os"
	"path/filepath"
)

// OpenRotated opens path for appending, rotating it to "<path>.1" first when
// it already exceeds maxBytes (single generation — the previous .1 is
// discarded). Parent directories are created as needed. A maxBytes <= 0
// disables rotation (file grows unbounded; callers should pass a real cap).
//
// Rotation happens once at startup, so a crash loop can't interleave
// generations; the file is opened with O_APPEND so all writes land at the end
// regardless of the ring/other writers.
func OpenRotated(path string, maxBytes int64) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	if maxBytes > 0 {
		if info, err := os.Stat(path); err == nil && info.Size() > maxBytes {
			rotated := path + ".1"
			_ = os.Remove(rotated) // best effort; may not exist
			if err := os.Rename(path, rotated); err != nil {
				return nil, fmt.Errorf("rotate %s: %w", path, err)
			}
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	return f, nil
}

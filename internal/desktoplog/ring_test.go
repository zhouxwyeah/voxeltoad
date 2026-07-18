package desktoplog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRing_AppendsAndTails(t *testing.T) {
	r := NewRing(10)
	for i := 0; i < 5; i++ {
		fmt.Fprintf(r, "line %d\n", i)
	}
	if r.Len() != 5 {
		t.Fatalf("Len = %d, want 5", r.Len())
	}
	tail := r.Tail(3)
	if len(tail) != 3 || tail[0] != "line 2" || tail[2] != "line 4" {
		t.Errorf("Tail(3) = %v, want [line 2 line 3 line 4]", tail)
	}
	// Tail larger than the buffer clamps.
	if all := r.Tail(100); len(all) != 5 {
		t.Errorf("Tail(100) len = %d, want 5", len(all))
	}
}

func TestRing_DropsOldestWhenFull(t *testing.T) {
	r := NewRing(3)
	for i := 0; i < 5; i++ {
		fmt.Fprintf(r, "line %d\n", i)
	}
	if r.Len() != 3 {
		t.Fatalf("Len = %d, want 3 (cap)", r.Len())
	}
	tail := r.Tail(3)
	if tail[0] != "line 2" || tail[2] != "line 4" {
		t.Errorf("tail = %v, want [line 2 line 3 line 4]", tail)
	}
}

func TestRing_PartialLineHeldUntilNewline(t *testing.T) {
	r := NewRing(10)
	_, _ = r.Write([]byte("partial"))
	if r.Len() != 0 {
		t.Fatalf("Len = %d, want 0 (unterminated line held)", r.Len())
	}
	_, _ = r.Write([]byte(" complete\nnext"))
	if got := r.Tail(10); len(got) != 1 || got[0] != "partial complete" {
		t.Errorf("tail = %v, want [partial complete]", got)
	}
	_, _ = r.Write([]byte(" line\n"))
	if got := r.Tail(10); len(got) != 2 || got[1] != "next line" {
		t.Errorf("tail = %v, want [partial complete next line]", got)
	}
}

func TestOpenRotated_RotatesOversizedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs", "desktop.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 1024)), 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := OpenRotated(path, 512) // existing 1KiB > 512 cap → rotate
	if err != nil {
		t.Fatalf("OpenRotated: %v", err)
	}
	defer f.Close()

	rotated, err := os.Stat(path + ".1")
	if err != nil || rotated.Size() != 1024 {
		t.Errorf("rotated file: %v size %d, want the old 1024 bytes at .1", err, rotated.Size())
	}
	fresh, err := os.Stat(path)
	if err != nil || fresh.Size() != 0 {
		t.Errorf("new log file: %v size %d, want a fresh empty file", err, fresh.Size())
	}
}

func TestOpenRotated_KeepsSmallFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "desktop.log")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := OpenRotated(path, 1<<20)
	if err != nil {
		t.Fatalf("OpenRotated: %v", err)
	}
	defer f.Close()
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Error("no rotation expected for a small file")
	}
	if _, err := fmt.Fprintln(f, "world"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "hello\nworld\n" {
		t.Errorf("file = %q, want appended content", b)
	}
}

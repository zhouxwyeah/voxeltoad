package desktoplog

import (
	"errors"
	"io"
	"testing"
)

type errWriter struct{ err error }

func (e errWriter) Write(p []byte) (int, error) { return 0, e.err }

type captureWriter struct{ n int }

func (c *captureWriter) Write(p []byte) (int, error) {
	c.n += len(p)
	return len(p), nil
}

// TestTee_WritesAllSinksDespiteError 复现 bug 触发条件：第一个 sink 报错
// （模拟 Windows GUI 进程下 os.Stderr 是无效句柄），后续 sink 仍必须被
// 写入。若有人把实现退回 io.MultiWriter，此测试会立刻失败。
func TestTee_WritesAllSinksDespiteError(t *testing.T) {
	errSink := errWriter{err: errors.New("invalid handle")}
	ring := &captureWriter{}
	file := &captureWriter{}
	tee := NewTee(errSink, ring, file)

	n, err := tee.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write err = %v, want nil (errors swallowed)", err)
	}
	if n != 5 {
		t.Fatalf("Write n = %d, want 5", n)
	}
	if ring.n != 5 {
		t.Error("ring sink not written — short-circuit bug")
	}
	if file.n != 5 {
		t.Error("file sink not written — short-circuit bug")
	}
}

func TestTee_SkipsNilWriters(t *testing.T) {
	cap := &captureWriter{}
	tee := NewTee(nil, cap, nil)
	if _, err := tee.Write([]byte("x")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if cap.n != 1 {
		t.Errorf("cap.n = %d, want 1", cap.n)
	}
}

// 编译期保证 Tee 实现 io.Writer。
var _ io.Writer = (*Tee)(nil)

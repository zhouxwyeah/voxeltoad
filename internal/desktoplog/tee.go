package desktoplog

import (
	"io"
	"sync"
)

// Tee 是一个容错的多路 writer：把每次 Write 广播到所有 sinks，任何一
// 个 sink 的错误都不会中断对其他 sink 的写入。
//
// 存在理由：io.MultiWriter 在第一个错误处短路 return。桌面网关在
// Windows GUI 进程下 os.Stderr 是无效句柄，会导致 logRing 和 logFile
// 永远收不到数据（文件被 OpenRotated 创建但 0 字节，UI Logs 页面也空）。
// Tee 把"扇出给多个 sink"做成容错：每个 sink 独立写，错误被丢弃
// （日志本身不应阻断业务路径，这与 Ring 的"never fails"契约一致）。
type Tee struct {
	mu      sync.Mutex
	writers []io.Writer
}

// NewTee 返回一个扇出到 writers 的 Tee。nil writer 被跳过，调用方在
// 某个 sink（如 logFile）不可用时无需分支判断。
func NewTee(writers ...io.Writer) *Tee {
	ws := make([]io.Writer, 0, len(writers))
	for _, w := range writers {
		if w != nil {
			ws = append(ws, w)
		}
	}
	return &Tee{writers: ws}
}

// Write 把 p 广播到所有 writer。返回 len(p), nil —— 任何 sink 的错误
// 都被吞掉（与 Ring 同样的"日志不能阻断业务"契约）。
func (t *Tee) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, w := range t.writers {
		_, _ = w.Write(p)
	}
	return len(p), nil
}

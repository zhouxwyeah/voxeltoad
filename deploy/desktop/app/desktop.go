package app

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"voxeltoad/internal/desktopapi"
)

// App is the Wails application context — the bridge between cmd/desktop/main.go's
// assembly (HTTP server + dispatcher + recorders) and the Wails window lifecycle.
// main.go constructs the *http.Server and passes it here; Run() blocks the main
// thread on wails.Run(), with the HTTP server started in OnStartup and stopped
// (gracefully) in OnShutdown.
//
// The window loads the SPA from the embedded Assets (deploy/desktop/app/assets.go),
// and /api/v1/* + /v1/* requests from the webview are proxied to the local HTTP
// server via AssetServer.Handler. External Agent processes hit the HTTP server
// directly at http://127.0.0.1:<port>/v1 — they never touch the webview.
type App struct {
	HTTPServer *http.Server
	// Listener is the gateway port pre-bound by desktopapp.Main (the bind
	// doubles as the port-conflict probe). ListenErr carries the failure when
	// the pre-bind failed; in that case no HTTP server is started and the user
	// gets a native dialog with remediation steps instead of a silent crash.
	Listener  net.Listener
	ListenErr error

	GatewayURL string // e.g. "http://127.0.0.1:8787" — for the AssetServer reverse proxy
	OnReload   func() error
	CfgPath    string // for the "Open config folder" menu item
	// KeyState is shared with the read API: it holds the current plaintext
	// key so the "复制 API key" menu item copies the REAL key (it previously
	// copied a hardcoded constant that went stale on env override or
	// rotation). known=false after a restart with a rotated key — the menu
	// item then explains instead of copying something wrong.
	KeyState *desktopapi.KeyState

	ctx context.Context
}

// Run builds the Wails options and blocks on wails.Run(). main.go calls this
// as its final statement; the HTTP server is started/stopped by the lifecycle
// hooks below, NOT before Run() — this keeps startup ordering simple (Wails
// owns the main thread, HTTP server runs in a goroutine under OnStartup).
func (a *App) Run() error {
	// Reverse proxy: webview → http://127.0.0.1:<port>/{api,v1}/*.
	// Needed because the webview origin is wails://wails.localhost (the
	// embedded SPA), so fetch('/api/v1/...') can't reach the HTTP server
	// directly. AssetServer.Handler is invoked for every non-GET request AND
	// every GET that the embedded Assets can't satisfy (i.e. /api/v1 and /v1).
	target, err := url.Parse(a.GatewayURL)
	if err != nil {
		return err
	}
	proxy := httputil.NewSingleHostReverseProxy(target)

	assetsFS, err := fs.Sub(Assets, "dist")
	if err != nil {
		// Should never happen — Assets embeds dist/ via //go:embed all:dist.
		return err
	}

	return wails.Run(&options.App{
		Title:             "桌面网关助手",
		Width:             1280,
		Height:            800,
		MinWidth:          900,
		MinHeight:         600,
		HideWindowOnClose: true, // dock close button → hide (keeps HTTP server alive for Agents)
		// Single instance: double-clicking the .app while it is already running
		// (possibly hidden to the dock) must not start a second gateway — the
		// pre-bound port would fail anyway. Focus the existing window instead.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "dev.voxeltoad.desktop",
			OnSecondInstanceLaunch: func(_ options.SecondInstanceData) {
				if a.ctx != nil {
					wailsruntime.WindowUnminimise(a.ctx)
					wailsruntime.WindowShow(a.ctx)
				}
			},
		},
		AssetServer: &assetserver.Options{
			Assets:  assetsFS,
			Handler: http.HandlerFunc(proxyHandler(proxy)),
		},
		Menu:       a.nativeMenu(),
		OnStartup:  a.onStartup,
		OnDomReady: func(ctx context.Context) { /* no-op */ },
		OnShutdown: a.onShutdown,
		OnBeforeClose: func(ctx context.Context) bool {
			// Hide instead of quitting when the window's close button is clicked.
			// The HTTP server stays up (Agents can keep calling); user quits via
			// the dock icon or the menu bar → 桌面网关助手 → 退出 (which triggers
			// OnShutdown through a different path).
			wailsruntime.WindowHide(ctx)
			return true // prevent the close
		},
		Bind: []any{a},
	})
}

// proxyHandler routes a request to the reverse proxy if it targets /api/v1/ or
// /v1/; otherwise returns 404.
func proxyHandler(proxy *httputil.ReverseProxy) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/") || strings.HasPrefix(r.URL.Path, "/v1/") {
			proxy.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	}
}

func (a *App) onStartup(ctx context.Context) {
	a.ctx = ctx
	// Port pre-bind failed in Main (e.g. a leftover process or another app on
	// the port). A GUI user would never see a stderr log.Fatalf, so surface
	// the assembled remediation steps in a native dialog and quit cleanly.
	if a.ListenErr != nil {
		_, _ = wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
			Type:    wailsruntime.ErrorDialog,
			Title:   "无法启动桌面网关",
			Message: a.ListenErr.Error(),
		})
		wailsruntime.Quit(ctx)
		return
	}
	// Start the HTTP server in a goroutine. Wails owns the main thread (wails.Run
	// blocks on it); the server handles Agent traffic + the webview's proxied
	// /api/v1 + /v1 requests concurrently.
	go func() {
		log.Printf("desktop gateway listening on %s", a.HTTPServer.Addr)
		if err := a.HTTPServer.Serve(a.Listener); err != nil && err != http.ErrServerClosed {
			log.Printf("serve failed: %v", err)
			_, _ = wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
				Type:    wailsruntime.ErrorDialog,
				Title:   "桌面网关服务异常退出",
				Message: fmt.Sprintf("HTTP 服务已停止：%v\n\n应用即将退出。", err),
			})
			wailsruntime.Quit(ctx)
		}
	}()
}

func (a *App) onShutdown(ctx context.Context) {
	// Graceful shutdown: drains in-flight requests (including SSE streams the
	// recorder is capturing), flushes the async recorders via their Close
	// (main.go sets up defer reqRec.Close()/traceRec.Close() — but those run on
	// main return; we do the HTTP server shutdown here so the window closes
	// promptly, and SQLite WAL checkpoints when db.Close() runs in main.go's
	// defer chain after Run() returns).
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.HTTPServer.Shutdown(shutCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
	log.Println("desktop gateway stopped")
}

// Reload is the bound method invoked by the "重载配置" menu item. It re-reads
// the YAML and rebuilds the dispatcher (hot-reload, design/desktop.md §7).
func (a *App) Reload() string {
	if a.OnReload == nil {
		return "reload not configured"
	}
	if err := a.OnReload(); err != nil {
		return "reload failed: " + err.Error()
	}
	return "reloaded"
}

// nativeMenu builds the macOS menu bar: app menu (quit) + Edit + custom
// "视图" submenu (reload config, open config folder, copy API key). The
// standard AppMenu and EditMenu come from Wails' menuroles (they wire up
// the OS-standard Copy/Paste/Quit/Minimize behaviors for free on macOS).
func (a *App) nativeMenu() *menu.Menu {
	viewMenu := menu.NewMenu()
	viewMenu.AddText("重载配置", keys.CmdOrCtrl("r"), func(_ *menu.CallbackData) {
		// Emit an event the frontend can listen for (optional, to refresh UI);
		// the authoritative reload happens in App.Reload() via the backend.
		if a.ctx != nil {
			wailsruntime.EventsEmit(a.ctx, "config:reloaded")
		}
		_ = a.Reload()
	})
	viewMenu.AddSeparator()
	viewMenu.AddText("打开配置文件位置", nil, func(_ *menu.CallbackData) {
		a.openConfigFolder()
	})
	viewMenu.AddText("复制 API key", keys.CmdOrCtrl("shift+k"), func(_ *menu.CallbackData) {
		if a.ctx == nil {
			return
		}
		if a.KeyState == nil {
			return
		}
		if plaintext, known := a.KeyState.Get(); known {
			_ = wailsruntime.ClipboardSetText(a.ctx, plaintext)
			return
		}
		// The key was rotated and the process restarted since — the plaintext
		// is unrecoverable from the stored hash. Point the user at the rotate
		// flow instead of copying a wrong/empty value.
		_, _ = wailsruntime.MessageDialog(a.ctx, wailsruntime.MessageDialogOptions{
			Type:    wailsruntime.InfoDialog,
			Title:   "API key 未知",
			Message: "当前密钥是轮换后重启的，明文已不可恢复。\n如需新密钥，请在 设置 → API 密钥 中再次轮换（旧密钥将失效）。",
		})
	})

	customView := &menu.MenuItem{
		Label:   "视图",
		Type:    menu.SubmenuType,
		SubMenu: viewMenu,
	}
	return menu.NewMenuFromItems(menu.AppMenu(), menu.EditMenu(), customView)
}

// openConfigFolder reveals the YAML config file in Finder via `open -R`.
func (a *App) openConfigFolder() {
	if a.CfgPath == "" {
		return
	}
	// `open -R <path>` reveals the file in Finder (macOS only — guarded by the
	// //go:build desktop tag, which is macOS-only in this iteration).
	_ = exec.Command("open", "-R", a.CfgPath).Run()
}

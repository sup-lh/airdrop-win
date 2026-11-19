package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	airdropwin "airdrop-win"
	"airdrop-win/internal/config"
	"airdrop-win/internal/network"
	"airdrop-win/internal/server"
	"airdrop-win/internal/transfer"
	"airdrop-win/internal/winutil"
)

func main() {
	portFlag := flag.Int("port", 0, "监听端口（默认使用配置文件中的端口 53318）")
	minimized := flag.Bool("minimized", false, "静默启动到系统托盘，不自动弹出主窗口")
	noTray := flag.Bool("no-tray", false, "禁用系统托盘（适合命令行或测试模式）")
	noOpen := flag.Bool("no-open", false, "启动时不自动打开主界面窗口")
	sendMode := flag.Bool("send", false, "将后续文件路径发送到隔空传共享队列")
	flag.Parse()

	sendPaths := flag.Args()

	cfgMgr, err := config.NewManager()
	if err != nil {
		log.Fatalf("初始化配置失败: %v", err)
	}
	cfg := cfgMgr.Get()
	targetPort := cfg.Port
	if *portFlag > 0 {
		targetPort = *portFlag
	}

	// Single-instance handoff: check if AirDropWin is already running on targetPort
	if *portFlag == 0 && tryHandoffRunningInstance(targetPort, *sendMode || len(sendPaths) > 0, sendPaths, !*minimized && !*noOpen) {
		return
	}

	dataDir := config.DefaultDir()
	iconICO, iconPNG := winutil.EnsureAppIcon(dataDir)

	store := transfer.NewStore(dataDir)
	hub := transfer.NewHub()

	var srv *server.Server
	openDashboard := func() {
		if srv == nil {
			return
		}
		url := fmt.Sprintf("http://127.0.0.1:%d/", srv.Port())
		winutil.OpenDashboardWindow(url)
	}

	srv = server.New(cfgMgr, store, hub, airdropwin.WebFS(), dataDir, iconICO, iconPNG, openDashboard)

	ln, actualPort, err := srv.Start(targetPort)
	if err != nil {
		log.Fatalf("启动局域网服务失败: %v", err)
	}

	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 30 * time.Second,
		// ReadTimeout & WriteTimeout intentionally 0 to support multi-GB streaming uploads/downloads
	}

	// Queue any files passed via --send or Explorer SendTo
	if len(sendPaths) > 0 {
		var absPaths []string
		for _, p := range sendPaths {
			if abs, err := filepath.Abs(p); err == nil {
				absPaths = append(absPaths, abs)
			}
		}
		srv.AddLocalFilesToOutbox(absPaths)
	}

	activeAdapter, _ := network.PickActiveAdapter(cfg.SelectedInterface)
	log.Printf("AirDropWin 已启动 | 本机控制台: http://127.0.0.1:%d | 局域网地址: http://%s:%d", actualPort, activeAdapter.IP, actualPort)

	shutdownCh := make(chan struct{})
	triggerShutdown := func() {
		select {
		case <-shutdownCh:
		default:
			close(shutdownCh)
		}
	}

	// Start pure-Go Windows System Tray
	var tray *winutil.Tray
	if !*noTray {
		tray = winutil.RunTray(winutil.TrayCallbacks{
			Tooltip:         fmt.Sprintf("隔空传 (AirDropWin) - %s:%d", activeAdapter.IP, actualPort),
			IconPath:        iconICO,
			OnOpenDashboard: openDashboard,
			OnSendFiles: func() {
				files, err := winutil.PickFilesDialog()
				if err == nil && len(files) > 0 {
					srv.AddLocalFilesToOutbox(files)
				}
			},
			OnPushClipboard: func() {
				item, err := srv.PushCurrentPCClipboard()
				if err != nil {
					winutil.ShowToast("隔空传剪贴板", err.Error(), iconPNG, "")
				} else if item != nil {
					msg := "已推送当前剪贴板内容到手机/Mac"
					if item.Kind == "image" {
						msg = "已推送剪贴板截图到手机/Mac"
					}
					winutil.ShowToast("剪贴板已推送", msg, iconPNG, "")
				}
			},
			OnOpenFolder: func() {
				dir := cfgMgr.Get().SaveDir
				_ = os.MkdirAll(dir, 0o755)
				_ = winutil.OpenPath(dir)
			},
			IsAutostart: winutil.IsAutostartEnabled,
			OnToggleAuto: func() {
				_ = winutil.SetAutostart(!winutil.IsAutostartEnabled())
			},
			IsSendTo: winutil.IsSendToEnabled,
			OnToggleSendTo: func() {
				_ = winutil.SetSendTo(!winutil.IsSendToEnabled(), iconICO)
			},
			OnQuit: triggerShutdown,
		})
	}

	// Start HTTP serving goroutine
	go func() {
		if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP 服务异常退出: %v", err)
			triggerShutdown()
		}
	}()

	// Open PC dashboard window automatically unless --minimized or --no-open
	if !*minimized && !*noOpen && cfg.OpenWindowOnStart {
		go func() {
			time.Sleep(200 * time.Millisecond)
			openDashboard()
		}()
	}

	// Wait for Ctrl+C / SIGTERM or Tray Quit
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case <-sigCh:
	case <-shutdownCh:
	}

	if tray != nil {
		tray.Quit()
	}
	srv.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}

func tryHandoffRunningInstance(port int, hasSend bool, sendPaths []string, bringToFront bool) bool {
	client := &http.Client{Timeout: 800 * time.Millisecond}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	resp, err := client.Get(base + "/api/ping")
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	var ping struct {
		OK  bool   `json:"ok"`
		App string `json:"app"`
	}
	if json.NewDecoder(resp.Body).Decode(&ping) != nil || !ping.OK || ping.App != "AirDropWin" {
		return false
	}

	if hasSend && len(sendPaths) > 0 {
		var absPaths []string
		for _, p := range sendPaths {
			if abs, err := filepath.Abs(p); err == nil {
				absPaths = append(absPaths, abs)
			}
		}
		payload, _ := json.Marshal(map[string]any{"paths": absPaths})
		r, err := client.Post(base+"/api/local/add-paths", "application/json", bytes.NewReader(payload))
		if err == nil {
			_ = r.Body.Close()
		}
		return true
	}

	if bringToFront {
		r, err := client.Post(base+"/api/local/bring-to-front", "application/json", nil)
		if err == nil {
			_ = r.Body.Close()
		}
	}
	return true
}

package server

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"airdrop-win/internal/clipboard"
	"airdrop-win/internal/config"
	"airdrop-win/internal/network"
	"airdrop-win/internal/transfer"
	"airdrop-win/internal/winutil"

	"github.com/gorilla/websocket"
)

type Server struct {
	cfgMgr   *config.Manager
	store    *transfer.Store
	hub      *transfer.Hub
	mdns     *network.MDNSAdvertiser
	webFS    fs.FS
	dataDir  string
	iconICO  string
	iconPNG  string
	outboxDir string

	mu            sync.Mutex
	activePort    int
	lastClipSeq   uint32
	lastClipText  string
	onOpenWindow  func()
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func New(cfgMgr *config.Manager, store *transfer.Store, hub *transfer.Hub, webFS fs.FS, dataDir, iconICO, iconPNG string, onOpenWindow func()) *Server {
	outbox := filepath.Join(dataDir, "outbox_cache")
	_ = os.MkdirAll(outbox, 0o755)
	s := &Server{
		cfgMgr:       cfgMgr,
		store:        store,
		hub:          hub,
		mdns:         network.NewMDNSAdvertiser(),
		webFS:        webFS,
		dataDir:      dataDir,
		iconICO:      iconICO,
		iconPNG:      iconPNG,
		outboxDir:    outbox,
		lastClipSeq:  clipboard.SequenceNumber(),
		onOpenWindow: onOpenWindow,
	}
	return s
}

func (s *Server) Start(port int) (net.Listener, int, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		// Fallback to another port if requested port is busy
		ln, err = net.Listen("tcp", "0.0.0.0:0")
		if err != nil {
			return nil, 0, err
		}
	}
	actualPort := ln.Addr().(*net.TCPAddr).Port
	s.mu.Lock()
	s.activePort = actualPort
	s.mu.Unlock()

	s.refreshMDNS()
	go s.clipboardWatcherLoop()

	return ln, actualPort, nil
}

func (s *Server) Stop() {
	s.mdns.Stop()
}

func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activePort
}

func (s *Server) refreshMDNS() {
	cfg := s.cfgMgr.Get()
	adapter, _ := network.PickActiveAdapter(cfg.SelectedInterface)
	s.mdns.Start(cfg.DeviceName, adapter.IP, s.Port())
}

func (s *Server) clipboardWatcherLoop() {
	ticker := time.NewTicker(900 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		cfg := s.cfgMgr.Get()
		if !cfg.ClipboardAutoPush {
			s.lastClipSeq = clipboard.SequenceNumber()
			continue
		}
		seq := clipboard.SequenceNumber()
		if seq == 0 || seq == s.lastClipSeq {
			continue
		}
		s.lastClipSeq = seq

		// Prefer text; if empty, check image
		if txt, err := clipboard.ReadText(); err == nil && strings.TrimSpace(txt) != "" {
			if txt == s.lastClipText {
				continue
			}
			s.lastClipText = txt
			item := s.store.AddClipboard(transfer.ClipboardItem{
				Kind:       "text",
				Text:       txt,
				Source:     "pc",
				DeviceName: cfg.DeviceName,
			})
			s.hub.Broadcast(transfer.Event{Type: "clipboard_new", Data: item})
			continue
		}
		if imgBytes, err := clipboard.ReadImagePNG(); err == nil && len(imgBytes) > 0 {
			id := transfer.NewID()
			imgPath := filepath.Join(s.store.ClipImageDir(), id+".png")
			if os.WriteFile(imgPath, imgBytes, 0o644) == nil {
				item := s.store.AddClipboard(transfer.ClipboardItem{
					ID:         id,
					Kind:       "image",
					ImagePath:  imgPath,
					MimeType:   "image/png",
					Source:     "pc",
					DeviceName: cfg.DeviceName,
				})
				s.hub.Broadcast(transfer.Event{Type: "clipboard_new", Data: item})
			}
		}
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Health & Pairing
	mux.HandleFunc("GET /api/ping", s.handlePing)
	mux.HandleFunc("POST /api/pair", s.handlePair)

	// WebSocket
	mux.HandleFunc("GET /ws", s.handleWS)

	// Localhost-only PC management APIs
	mux.HandleFunc("GET /api/local/bootstrap", s.requireLocal(s.handleLocalBootstrap))
	mux.HandleFunc("GET /api/local/qr.png", s.requireLocal(s.handleLocalQR))
	mux.HandleFunc("POST /api/local/config", s.requireLocal(s.handleLocalUpdateConfig))
	mux.HandleFunc("POST /api/local/token/reset", s.requireLocal(s.handleLocalResetToken))
	mux.HandleFunc("POST /api/local/pick-files", s.requireLocal(s.handleLocalPickFiles))
	mux.HandleFunc("POST /api/local/add-paths", s.requireLocal(s.handleLocalAddPaths))
	mux.HandleFunc("POST /api/local/upload-outbox", s.requireLocal(s.handleLocalUploadOutbox))
	mux.HandleFunc("POST /api/local/pick-folder", s.requireLocal(s.handleLocalPickFolder))
	mux.HandleFunc("POST /api/local/clipboard/push", s.requireLocal(s.handleLocalPushClipboard))
	mux.HandleFunc("POST /api/local/clipboard/copy/{id}", s.requireLocal(s.handleLocalCopyClipboardItem))
	mux.HandleFunc("POST /api/local/pending/{id}/decision", s.requireLocal(s.handleLocalPendingDecision))
	mux.HandleFunc("POST /api/local/open/{id}", s.requireLocal(s.handleLocalOpenTransfer))
	mux.HandleFunc("POST /api/local/reveal/{id}", s.requireLocal(s.handleLocalRevealTransfer))
	mux.HandleFunc("POST /api/local/open-savedir", s.requireLocal(s.handleLocalOpenSaveDir))
	mux.HandleFunc("POST /api/local/toggle-autostart", s.requireLocal(s.handleLocalToggleAutostart))
	mux.HandleFunc("POST /api/local/toggle-sendto", s.requireLocal(s.handleLocalToggleSendTo))
	mux.HandleFunc("POST /api/local/firewall/allow", s.requireLocal(s.handleLocalFirewallAllow))
	mux.HandleFunc("DELETE /api/local/transfer/{id}", s.requireLocal(s.handleLocalDeleteTransfer))
	mux.HandleFunc("POST /api/local/transfers/clear", s.requireLocal(s.handleLocalClearTransfers))
	mux.HandleFunc("DELETE /api/local/clipboard/{id}", s.requireLocal(s.handleLocalDeleteClipboard))
	mux.HandleFunc("POST /api/local/clipboard/clear", s.requireLocal(s.handleLocalClearClipboard))
	mux.HandleFunc("POST /api/local/bring-to-front", s.requireLocal(s.handleLocalBringToFront))

	// Remote (iPhone / Mac) APIs
	mux.HandleFunc("GET /api/remote/bootstrap", s.requireAuth(s.handleRemoteBootstrap))
	mux.HandleFunc("POST /api/remote/request", s.requireAuth(s.handleRemoteRequest))
	mux.HandleFunc("POST /api/remote/upload", s.requireAuth(s.handleRemoteUpload))
	mux.HandleFunc("GET /api/files/{id}", s.requireAuth(s.handleDownloadFile))
	mux.HandleFunc("GET /api/clipboard/{id}/image", s.requireAuth(s.handleClipboardImage))
	mux.HandleFunc("POST /api/clipboard/push", s.requireAuth(s.handleRemoteClipboardPush))
	mux.HandleFunc("GET /api/clipboard/latest", s.requireAuth(s.handleClipboardLatest))

	// Apple Shortcuts Dedicated APIs
	mux.HandleFunc("GET /api/shortcut/ping", s.requireAuth(s.handleShortcutPing))
	mux.HandleFunc("POST /api/shortcut/upload", s.requireAuth(s.handleShortcutUpload))
	mux.HandleFunc("POST /api/shortcut/clipboard", s.requireAuth(s.handleShortcutClipboardSet))
	mux.HandleFunc("GET /api/shortcut/clipboard", s.requireAuth(s.handleShortcutClipboardGet))

	// Embedded frontend static assets & SPA fallback
	fileServer := http.FileServer(http.FS(s.webFS))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// If pairing token is in query string (?t=<token>), validate and set HttpOnly cookie right away!
		if tok := r.URL.Query().Get("t"); tok != "" {
			if s.matchToken(tok) {
				setAuthCookie(w, tok)
			}
		}

		cleanPath := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(r.URL.Path)), "/")
		if cleanPath == "" || cleanPath == "." {
			cleanPath = "index.html"
		}
		if f, err := s.webFS.Open(cleanPath); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback
		indexBytes, err := fs.ReadFile(s.webFS, "index.html")
		if err != nil {
			http.Error(w, "UI not built", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexBytes)
	})

	return withCORS(mux)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Token, Authorization, X-Filename, X-Device, X-Request-Id")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLocalRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) matchToken(candidate string) bool {
	if candidate == "" {
		return false
	}
	expected := s.cfgMgr.Get().Token
	return len(candidate) == len(expected) && subtle.ConstantTimeCompare([]byte(candidate), []byte(expected)) == 1
}

func (s *Server) extractToken(r *http.Request) string {
	if t := strings.TrimSpace(r.Header.Get("X-Token")); t != "" {
		return t
	}
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	if t := strings.TrimSpace(r.URL.Query().Get("token")); t != "" {
		return t
	}
	if t := strings.TrimSpace(r.URL.Query().Get("t")); t != "" {
		return t
	}
	if c, err := r.Cookie("airdrop_token"); err == nil && c.Value != "" {
		return c.Value
	}
	return ""
}

func (s *Server) isAuthorized(r *http.Request) bool {
	if isLocalRequest(r) {
		return true
	}
	return s.matchToken(s.extractToken(r))
}

func (s *Server) requireLocal(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLocalRequest(r) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "仅允许本机 Windows 访问此接口"})
			return
		}
		next(w, r)
	}
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isAuthorized(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "未配对或访问令牌已失效，请重新扫码"})
			return
		}
		next(w, r)
	}
}

func setAuthCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "airdrop_token",
		Value:    token,
		Path:     "/",
		MaxAge:   365 * 24 * 3600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"app":        "AirDropWin",
		"deviceName": cfg.DeviceName,
		"isLocal":    isLocalRequest(r),
		"authorized": s.isAuthorized(r),
	})
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求格式无效"})
		return
	}
	if !s.matchToken(strings.TrimSpace(body.Token)) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "配对令牌不正确，请重新扫码"})
		return
	}
	setAuthCookie(w, strings.TrimSpace(body.Token))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"deviceName": s.cfgMgr.Get().DeviceName,
	})
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if !s.isAuthorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ws] upgrade error: %v", err)
		return
	}

	role := r.URL.Query().Get("role")
	if role == "pc" && !isLocalRequest(r) {
		role = "remote"
	}
	if role != "pc" {
		role = "remote"
	}

	deviceName := strings.TrimSpace(r.URL.Query().Get("name"))
	platform := detectPlatform(r.UserAgent())
	if deviceName == "" {
		deviceName = defaultDeviceLabel(platform)
	}

	client := s.hub.Register(conn, transfer.ClientInfo{
		Role:       role,
		DeviceName: deviceName,
		Platform:   platform,
		IP:         remoteIP(r),
	})
	client.ReadPump()
}

func detectPlatform(ua string) string {
	u := strings.ToLower(ua)
	switch {
	case strings.Contains(u, "iphone"):
		return "ios"
	case strings.Contains(u, "ipad"):
		return "ipad"
	case strings.Contains(u, "macintosh") || strings.Contains(u, "mac os x"):
		return "macos"
	case strings.Contains(u, "android"):
		return "android"
	case strings.Contains(u, "windows"):
		return "windows"
	default:
		return "web"
	}
}

func defaultDeviceLabel(platform string) string {
	switch platform {
	case "ios":
		return "iPhone"
	case "ipad":
		return "iPad"
	case "macos":
		return "Mac"
	case "android":
		return "Android 手机"
	case "windows":
		return "Windows 设备"
	default:
		return "局域网设备"
	}
}

// AddLocalFilesToOutbox shares one or more local disk paths zero-copy to connected remote devices.
func (s *Server) AddLocalFilesToOutbox(paths []string) []transfer.TransferItem {
	cfg := s.cfgMgr.Get()
	var added []transfer.TransferItem
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(p))
		kind := "file"
		mimeType := "application/octet-stream"
		switch ext {
		case ".jpg", ".jpeg":
			kind, mimeType = "image", "image/jpeg"
		case ".png":
			kind, mimeType = "image", "image/png"
		case ".gif":
			kind, mimeType = "image", "image/gif"
		case ".webp":
			kind, mimeType = "image", "image/webp"
		case ".heic":
			kind, mimeType = "image", "image/heic"
		case ".mp4":
			mimeType = "video/mp4"
		case ".mov":
			mimeType = "video/quicktime"
		case ".pdf":
			mimeType = "application/pdf"
		case ".zip":
			mimeType = "application/zip"
		}
		item := s.store.AddTransfer(transfer.TransferItem{
			Direction:  "out",
			Kind:       kind,
			Name:       filepath.Base(p),
			Size:       info.Size(),
			MimeType:   mimeType,
			LocalPath:  p,
			DeviceName: cfg.DeviceName,
			Status:     "done",
		})
		s.hub.Broadcast(transfer.Event{Type: "transfer_new", Data: item})
		added = append(added, item)
	}
	if len(added) > 0 && cfg.NotifyOnReceive {
		msg := fmt.Sprintf("已准备发送 %d 个文件，手机/Mac 端可直接下载", len(added))
		if len(added) == 1 {
			msg = fmt.Sprintf("已推送「%s」到隔空传队列，请在手机/Mac 端下载", added[0].Name)
		}
		winutil.ShowToast("隔空传已就绪", msg, s.iconPNG, "")
	}
	return added
}

// PushCurrentPCClipboard reads Windows clipboard (text or image) and broadcasts to remote devices.
func (s *Server) PushCurrentPCClipboard() (*transfer.ClipboardItem, error) {
	cfg := s.cfgMgr.Get()
	if txt, err := clipboard.ReadText(); err == nil && strings.TrimSpace(txt) != "" {
		s.lastClipText = txt
		s.lastClipSeq = clipboard.SequenceNumber()
		item := s.store.AddClipboard(transfer.ClipboardItem{
			Kind:       "text",
			Text:       txt,
			Source:     "pc",
			DeviceName: cfg.DeviceName,
		})
		s.hub.Broadcast(transfer.Event{Type: "clipboard_new", Data: item})
		return &item, nil
	}
	if imgBytes, err := clipboard.ReadImagePNG(); err == nil && len(imgBytes) > 0 {
		s.lastClipSeq = clipboard.SequenceNumber()
		id := transfer.NewID()
		imgPath := filepath.Join(s.store.ClipImageDir(), id+".png")
		if err := os.WriteFile(imgPath, imgBytes, 0o644); err != nil {
			return nil, err
		}
		item := s.store.AddClipboard(transfer.ClipboardItem{
			ID:         id,
			Kind:       "image",
			ImagePath:  imgPath,
			MimeType:   "image/png",
			Source:     "pc",
			DeviceName: cfg.DeviceName,
		})
		s.hub.Broadcast(transfer.Event{Type: "clipboard_new", Data: item})
		return &item, nil
	}
	return nil, fmt.Errorf("当前 Windows 剪贴板为空（无文本或图片）")
}

package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"airdrop-win/internal/clipboard"
	"airdrop-win/internal/config"
	"airdrop-win/internal/network"
	"airdrop-win/internal/transfer"
	"airdrop-win/internal/winutil"

	qrcode "github.com/skip2/go-qrcode"
)

func (s *Server) handleLocalBootstrap(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	active, adapters := network.PickActiveAdapter(cfg.SelectedInterface)
	port := s.Port()

	baseURL := fmt.Sprintf("http://%s:%d", active.IP, port)
	pairURL := fmt.Sprintf("%s/?t=%s", baseURL, cfg.Token)

	mdnsHost := s.mdns.Hostname()
	mdnsPairURL := ""
	if mdnsHost != "" {
		mdnsPairURL = fmt.Sprintf("http://%s:%d/?t=%s", mdnsHost, port, cfg.Token)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"config":        cfg,
		"port":          port,
		"activeAdapter": active,
		"adapters":      adapters,
		"baseUrl":       baseURL,
		"pairUrl":       pairURL,
		"mdnsHost":      mdnsHost,
		"mdnsPairUrl":   mdnsPairURL,
		"autostart":     winutil.IsAutostartEnabled(),
		"sendTo":        winutil.IsSendToEnabled(),
		"firewallOk":    winutil.CheckFirewallRule(),
		"devices":       s.hub.Devices(),
		"transfers":     s.store.ListTransfers(),
		"clipboard":     s.store.ListClipboard(),
		"pending":       s.store.ListPending(),
		"shortcutLogs":  s.store.ListShortcutLogs(),
	})
}

func (s *Server) handleLocalQR(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	active, _ := network.PickActiveAdapter(cfg.SelectedInterface)
	port := s.Port()

	target := fmt.Sprintf("http://%s:%d/?t=%s", active.IP, port, cfg.Token)
	if r.URL.Query().Get("mode") == "mdns" && s.mdns.Hostname() != "" {
		target = fmt.Sprintf("http://%s:%d/?t=%s", s.mdns.Hostname(), port, cfg.Token)
	}

	pngBytes, err := qrcode.Encode(target, qrcode.Medium, 320)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(pngBytes)
}

func (s *Server) handleLocalUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceName        *string `json:"deviceName"`
		SaveDir           *string `json:"saveDir"`
		ConfirmReceive    *bool   `json:"confirmReceive"`
		AutoCopyText      *bool   `json:"autoCopyText"`
		ClipboardAutoPush *bool   `json:"clipboardAutoPush"`
		SelectedInterface *string `json:"selectedInterface"`
		NotifyOnReceive   *bool   `json:"notifyOnReceive"`
		OpenWindowOnStart *bool   `json:"openWindowOnStart"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "参数解析失败"})
		return
	}

	needMDNS := false
	updated, err := s.cfgMgr.Update(func(c *config.Config) {
		if req.DeviceName != nil && strings.TrimSpace(*req.DeviceName) != "" {
			if c.DeviceName != strings.TrimSpace(*req.DeviceName) {
				needMDNS = true
			}
			c.DeviceName = strings.TrimSpace(*req.DeviceName)
		}
		if req.SaveDir != nil && strings.TrimSpace(*req.SaveDir) != "" {
			c.SaveDir = strings.TrimSpace(*req.SaveDir)
		}
		if req.ConfirmReceive != nil {
			c.ConfirmReceive = *req.ConfirmReceive
		}
		if req.AutoCopyText != nil {
			c.AutoCopyText = *req.AutoCopyText
		}
		if req.ClipboardAutoPush != nil {
			c.ClipboardAutoPush = *req.ClipboardAutoPush
		}
		if req.SelectedInterface != nil {
			if c.SelectedInterface != *req.SelectedInterface {
				needMDNS = true
			}
			c.SelectedInterface = *req.SelectedInterface
		}
		if req.NotifyOnReceive != nil {
			c.NotifyOnReceive = *req.NotifyOnReceive
		}
		if req.OpenWindowOnStart != nil {
			c.OpenWindowOnStart = *req.OpenWindowOnStart
		}
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if needMDNS {
		go s.refreshMDNS()
	}
	s.hub.Broadcast(transfer.Event{Type: "config_updated", Data: map[string]any{
		"deviceName":     updated.DeviceName,
		"confirmReceive": updated.ConfirmReceive,
	}})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": updated})
}

func (s *Server) handleLocalResetToken(w http.ResponseWriter, r *http.Request) {
	tok, err := s.cfgMgr.ResetToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	s.hub.KickRemotes()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "token": tok})
}

func (s *Server) handleLocalPickFiles(w http.ResponseWriter, r *http.Request) {
	files, err := winutil.PickFilesDialog()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if len(files) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": []any{}})
		return
	}
	added := s.AddLocalFilesToOutbox(files)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": added})
}

func (s *Server) handleLocalAddPaths(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "无效的文件路径列表"})
		return
	}
	added := s.AddLocalFilesToOutbox(body.Paths)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": added})
}

func (s *Server) handleLocalUploadOutbox(w http.ResponseWriter, r *http.Request) {
	mr, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "需要 multipart/form-data 上传"})
		return
	}

	cfg := s.cfgMgr.Get()
	var added []transfer.TransferItem
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if part.FileName() == "" {
			_ = part.Close()
			continue
		}
		finalPath, finalName, written, mimeType, serr := transfer.StreamSaveToDir(
			s.outboxDir,
			part.FileName(),
			part,
			part.Header.Get("Content-Type"),
		)
		_ = part.Close()
		if serr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": serr.Error()})
			return
		}
		kind := "file"
		if strings.HasPrefix(mimeType, "image/") {
			kind = "image"
		}
		item := s.store.AddTransfer(transfer.TransferItem{
			Direction:  "out",
			Kind:       kind,
			Name:       finalName,
			Size:       written,
			MimeType:   mimeType,
			LocalPath:  finalPath,
			DeviceName: cfg.DeviceName,
			Status:     "done",
		})
		s.hub.Broadcast(transfer.Event{Type: "transfer_new", Data: item})
		added = append(added, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": added})
}

func (s *Server) handleLocalPickFolder(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	dir, err := winutil.PickFolderDialog(cfg.SaveDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if dir == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "saveDir": cfg.SaveDir})
		return
	}
	updated, err := s.cfgMgr.Update(func(c *config.Config) {
		c.SaveDir = dir
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "saveDir": updated.SaveDir})
}

func (s *Server) handleLocalPushClipboard(w http.ResponseWriter, r *http.Request) {
	// If request body contains explicit text, write to Windows clipboard + broadcast; otherwise read current Windows clipboard
	var body struct {
		Text string `json:"text"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if strings.TrimSpace(body.Text) != "" {
		cfg := s.cfgMgr.Get()
		_ = clipboard.WriteText(body.Text)
		s.lastClipText = body.Text
		s.lastClipSeq = clipboard.SequenceNumber()
		item := s.store.AddClipboard(transfer.ClipboardItem{
			Kind:       "text",
			Text:       body.Text,
			Source:     "pc",
			DeviceName: cfg.DeviceName,
		})
		s.hub.Broadcast(transfer.Event{Type: "clipboard_new", Data: item})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "item": item})
		return
	}

	item, err := s.PushCurrentPCClipboard()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "item": item})
}

func (s *Server) handleLocalCopyClipboardItem(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, ok := s.store.GetClipboard(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "剪贴板记录不存在"})
		return
	}
	if item.Kind == "image" && item.ImagePath != "" {
		imgBytes, err := os.ReadFile(item.ImagePath)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "图片文件已丢失"})
			return
		}
		if err := clipboard.WriteImage(imgBytes); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		s.lastClipSeq = clipboard.SequenceNumber()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if err := clipboard.WriteText(item.Text); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	s.lastClipText = item.Text
	s.lastClipSeq = clipboard.SequenceNumber()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLocalPendingDecision(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Accept bool `json:"accept"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "参数无效"})
		return
	}
	ok := s.store.ResolvePending(id, body.Accept)
	s.hub.BroadcastToRole("pc", transfer.Event{
		Type: "pending_resolved",
		Data: map[string]any{"id": id, "accept": body.Accept},
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok})
}

func (s *Server) handleLocalOpenTransfer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, ok := s.store.GetTransfer(id)
	if !ok || item.LocalPath == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "文件不存在"})
		return
	}
	if err := winutil.OpenPath(item.LocalPath); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLocalRevealTransfer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, ok := s.store.GetTransfer(id)
	if !ok || item.LocalPath == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "文件不存在"})
		return
	}
	if err := winutil.RevealInExplorer(item.LocalPath); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLocalOpenSaveDir(w http.ResponseWriter, r *http.Request) {
	dir := s.cfgMgr.Get().SaveDir
	_ = os.MkdirAll(dir, 0o755)
	_ = winutil.OpenPath(dir)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLocalToggleAutostart(w http.ResponseWriter, r *http.Request) {
	target := !winutil.IsAutostartEnabled()
	if err := winutil.SetAutostart(target); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "autostart": winutil.IsAutostartEnabled()})
}

func (s *Server) handleLocalToggleSendTo(w http.ResponseWriter, r *http.Request) {
	target := !winutil.IsSendToEnabled()
	if err := winutil.SetSendTo(target, s.iconICO); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sendTo": winutil.IsSendToEnabled()})
}

func (s *Server) handleLocalFirewallAllow(w http.ResponseWriter, r *http.Request) {
	if err := winutil.AddFirewallRuleElevated(s.Port()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLocalDeleteTransfer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if item, ok := s.store.GetTransfer(id); ok {
		// Clean up browser-dragged cached outbox file if inside our cache dir (never delete original user files!)
		if item.LocalPath != "" && strings.HasPrefix(strings.ToLower(item.LocalPath), strings.ToLower(s.outboxDir)) {
			_ = os.Remove(item.LocalPath)
		}
	}
	s.store.DeleteTransfer(id)
	s.hub.Broadcast(transfer.Event{Type: "transfer_delete", Data: map[string]any{"id": id}})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLocalClearTransfers(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Direction string `json:"direction"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.store.ClearTransfers(body.Direction)
	s.hub.Broadcast(transfer.Event{Type: "transfers_cleared", Data: map[string]any{"direction": body.Direction}})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLocalDeleteClipboard(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.store.DeleteClipboard(id)
	s.hub.Broadcast(transfer.Event{Type: "clipboard_delete", Data: map[string]any{"id": id}})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLocalClearClipboard(w http.ResponseWriter, r *http.Request) {
	s.store.ClearClipboard()
	s.hub.Broadcast(transfer.Event{Type: "clipboard_cleared", Data: map[string]any{}})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLocalBringToFront(w http.ResponseWriter, r *http.Request) {
	if s.onOpenWindow != nil {
		go s.onOpenWindow()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

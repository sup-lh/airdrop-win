package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"airdrop-win/internal/clipboard"
	"airdrop-win/internal/transfer"
	"airdrop-win/internal/winutil"
)

func (s *Server) handleRemoteBootstrap(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	writeJSON(w, http.StatusOK, map[string]any{
		"pcName":         cfg.DeviceName,
		"confirmReceive": cfg.ConfirmReceive,
		"transfers":      s.store.ListTransfers(),
		"clipboard":      s.store.ListClipboard(),
	})
}

// handleRemoteRequest implements AirDrop-style pre-transfer Ask/Accept consent when ConfirmReceive is enabled.
func (s *Server) handleRemoteRequest(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	var body struct {
		DeviceName string              `json:"deviceName"`
		Files      []transfer.FileMeta `json:"files"`
		TotalSize  int64               `json:"totalSize"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求参数错误"})
		return
	}
	if body.DeviceName == "" {
		body.DeviceName = defaultDeviceLabel(detectPlatform(r.UserAgent()))
	}

	if !cfg.ConfirmReceive {
		req, _ := s.store.CreatePending(body.DeviceName, remoteIP(r), body.Files, body.TotalSize)
		s.store.ResolvePending(req.ID, true)
		writeJSON(w, http.StatusOK, map[string]any{"approved": true, "requestId": req.ID})
		return
	}

	req, ch := s.store.CreatePending(body.DeviceName, remoteIP(r), body.Files, body.TotalSize)
	s.hub.BroadcastToRole("pc", transfer.Event{
		Type: "pending_request",
		Data: req,
	})
	if cfg.NotifyOnReceive {
		summary := fmt.Sprintf("%s 请求发送 %d 个文件 (%s)", req.DeviceName, len(req.Files), formatBytes(req.TotalSize))
		winutil.ShowToast("隔空传接收请求", summary+" — 请在主界面点击接收或拒绝", s.iconPNG, "")
	}

	select {
	case accepted := <-ch:
		writeJSON(w, http.StatusOK, map[string]any{"approved": accepted, "requestId": req.ID})
	case <-time.After(60 * time.Second):
		s.store.CancelPending(req.ID)
		s.hub.BroadcastToRole("pc", transfer.Event{
			Type: "pending_resolved",
			Data: map[string]any{"id": req.ID, "accept": false, "timeout": true},
		})
		writeJSON(w, http.StatusRequestTimeout, map[string]any{"approved": false, "error": "等待电脑确认超时"})
	case <-r.Context().Done():
		s.store.CancelPending(req.ID)
		s.hub.BroadcastToRole("pc", transfer.Event{
			Type: "pending_resolved",
			Data: map[string]any{"id": req.ID, "accept": false, "cancelled": true},
		})
	}
}

// handleRemoteUpload streams uploaded files directly to disk without buffering in RAM.
func (s *Server) handleRemoteUpload(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	reqID := strings.TrimSpace(r.Header.Get("X-Request-Id"))
	if reqID == "" {
		reqID = strings.TrimSpace(r.URL.Query().Get("requestId"))
	}

	if cfg.ConfirmReceive && !s.store.IsApproved(reqID) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "电脑开启了接收确认，请先获得确认"})
		return
	}

	mr, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请使用 multipart/form-data 上传文件"})
		return
	}

	deviceName := decodeHeaderFilename(r.Header.Get("X-Device"))
	if deviceName == "" {
		deviceName = defaultDeviceLabel(detectPlatform(r.UserAgent()))
	}

	var saved []transfer.TransferItem
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "上传中断: " + err.Error()})
			return
		}
		if part.FormName() == "deviceName" && part.FileName() == "" {
			b, _ := io.ReadAll(io.LimitReader(part, 256))
			if sName := strings.TrimSpace(string(b)); sName != "" {
				deviceName = sName
			}
			_ = part.Close()
			continue
		}
		if part.FileName() == "" {
			_ = part.Close()
			continue
		}

		finalPath, finalName, written, mimeType, serr := transfer.StreamSaveToDir(
			cfg.SaveDir,
			part.FileName(),
			part,
			part.Header.Get("Content-Type"),
		)
		_ = part.Close()
		if serr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存文件失败: " + serr.Error()})
			return
		}

		kind := "file"
		if strings.HasPrefix(mimeType, "image/") {
			kind = "image"
		}
		item := s.store.AddTransfer(transfer.TransferItem{
			Direction:  "in",
			Kind:       kind,
			Name:       finalName,
			Size:       written,
			MimeType:   mimeType,
			LocalPath:  finalPath,
			DeviceName: deviceName,
			Status:     "done",
		})
		s.hub.Broadcast(transfer.Event{Type: "transfer_new", Data: item})
		saved = append(saved, item)
	}

	if len(saved) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "未检测到上传文件"})
		return
	}

	if cfg.NotifyOnReceive {
		if len(saved) == 1 {
			winutil.ShowToast(
				fmt.Sprintf("收到来自 %s 的文件", deviceName),
				fmt.Sprintf("%s (%s)", saved[0].Name, formatBytes(saved[0].Size)),
				s.iconPNG,
				saved[0].LocalPath,
			)
		} else {
			var total int64
			for _, it := range saved {
				total += it.Size
			}
			winutil.ShowToast(
				fmt.Sprintf("收到来自 %s 的 %d 个文件", deviceName, len(saved)),
				fmt.Sprintf("共 %s，已保存到 %s", formatBytes(total), cfg.SaveDir),
				s.iconPNG,
				saved[0].LocalPath,
			)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"items": saved,
	})
}

func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, ok := s.store.GetTransfer(id)
	if !ok || item.LocalPath == "" {
		http.Error(w, "文件不存在", http.StatusNotFound)
		return
	}
	f, err := os.Open(item.LocalPath)
	if err != nil {
		http.Error(w, "文件已被移动或删除", http.StatusNotFound)
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil || stat.IsDir() {
		http.Error(w, "无法读取文件", http.StatusNotFound)
		return
	}

	if item.MimeType != "" {
		w.Header().Set("Content-Type", item.MimeType)
	}

	disposition := "attachment"
	if r.URL.Query().Get("inline") == "1" {
		disposition = "inline"
	}
	// RFC 5987 UTF-8 filename ensures Chinese filenames download cleanly on iOS Safari & macOS Safari!
	encoded := url.PathEscape(item.Name)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, disposition, asciiFallback(item.Name), encoded))

	http.ServeContent(w, r, item.Name, stat.ModTime(), f)
}

func (s *Server) handleClipboardImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, ok := s.store.GetClipboard(id)
	if !ok || item.ImagePath == "" {
		http.Error(w, "图片不存在", http.StatusNotFound)
		return
	}
	http.ServeFile(w, r, item.ImagePath)
}

func (s *Server) handleRemoteClipboardPush(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	deviceName := decodeHeaderFilename(r.Header.Get("X-Device"))
	if deviceName == "" {
		deviceName = defaultDeviceLabel(detectPlatform(r.UserAgent()))
	}

	// Support image push via multipart or raw image/*
	if strings.HasPrefix(ct, "multipart/form-data") {
		mr, err := r.MultipartReader()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			if part.FileName() != "" {
				imgBytes, _ := io.ReadAll(io.LimitReader(part, 32*1024*1024))
				_ = part.Close()
				if len(imgBytes) == 0 {
					continue
				}
				id := transfer.NewID()
				imgPath := filepath.Join(s.store.ClipImageDir(), id+".png")
				_ = os.WriteFile(imgPath, imgBytes, 0o644)
				if cfg.AutoCopyText {
					_ = clipboard.WriteImage(imgBytes)
					s.lastClipSeq = clipboard.SequenceNumber()
				}
				item := s.store.AddClipboard(transfer.ClipboardItem{
					ID:         id,
					Kind:       "image",
					ImagePath:  imgPath,
					MimeType:   http.DetectContentType(imgBytes),
					Source:     "remote",
					DeviceName: deviceName,
				})
				s.hub.Broadcast(transfer.Event{Type: "clipboard_new", Data: item})
				if cfg.NotifyOnReceive {
					winutil.ShowToast(fmt.Sprintf("收到来自 %s 的图片剪贴板", deviceName), "已写入 Windows 剪贴板，可直接 Ctrl+V 粘贴", s.iconPNG, "")
				}
				writeJSON(w, http.StatusOK, map[string]any{"ok": true, "item": item})
				return
			}
			_ = part.Close()
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "未收到图片数据"})
		return
	}

	var body struct {
		Text       string `json:"text"`
		DeviceName string `json:"deviceName"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "无效的剪贴板请求"})
		return
	}
	if body.DeviceName != "" {
		deviceName = body.DeviceName
	}
	if strings.TrimSpace(body.Text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "剪贴板文本不能为空"})
		return
	}

	if cfg.AutoCopyText {
		_ = clipboard.WriteText(body.Text)
		s.lastClipText = body.Text
		s.lastClipSeq = clipboard.SequenceNumber()
	}

	item := s.store.AddClipboard(transfer.ClipboardItem{
		Kind:       "text",
		Text:       body.Text,
		Source:     "remote",
		DeviceName: deviceName,
	})
	s.hub.Broadcast(transfer.Event{Type: "clipboard_new", Data: item})
	if cfg.NotifyOnReceive {
		preview := body.Text
		if len([]rune(preview)) > 80 {
			preview = string([]rune(preview)[:80]) + "..."
		}
		winutil.ShowToast(fmt.Sprintf("剪贴板同步 (%s)", deviceName), preview, s.iconPNG, "")
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "item": item})
}

func (s *Server) handleClipboardLatest(w http.ResponseWriter, r *http.Request) {
	// Live-read current Windows clipboard text so iPhone always gets the latest Ctrl+C on PC!
	if txt, err := clipboard.ReadText(); err == nil && strings.TrimSpace(txt) != "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":   true,
			"kind": "text",
			"text": txt,
		})
		return
	}
	list := s.store.ListClipboard()
	if len(list) > 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":   true,
			"kind": list[0].Kind,
			"text": list[0].Text,
			"id":   list[0].ID,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "kind": "text", "text": ""})
}

func decodeHeaderFilename(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if unesc, err := url.PathUnescape(raw); err == nil && unesc != "" {
		return unesc
	}
	return raw
}

func asciiFallback(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r >= 0x20 && r <= 0x7e && r != '"' && r != '\\' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	res := strings.TrimSpace(b.String())
	if res == "" {
		return "download"
	}
	return res
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

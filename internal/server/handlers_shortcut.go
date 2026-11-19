package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"airdrop-win/internal/clipboard"
	"airdrop-win/internal/transfer"
	"airdrop-win/internal/winutil"
)

func (s *Server) logShortcut(r *http.Request, status int, msg string) {
	entry := s.store.AddShortcutLog(transfer.ShortcutLog{
		Method:   r.Method,
		Path:     r.URL.Path,
		RemoteIP: remoteIP(r),
		Status:   status,
		Message:  msg,
	})
	s.hub.BroadcastToRole("pc", transfer.Event{
		Type: "shortcut_log",
		Data: entry,
	})
}

func (s *Server) handleShortcutPing(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	s.logShortcut(r, http.StatusOK, "快捷指令连接测试成功")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"deviceName": cfg.DeviceName,
		"message":    fmt.Sprintf("已连接到 %s", cfg.DeviceName),
	})
}

// handleShortcutUpload is engineered for iOS/macOS Apple Shortcuts:
// - Supports raw binary body ("请求体: 文件") -> streams multi-GB 4K videos & ZIPs without OOM!
// - Supports multipart/form-data ("请求体: 表单")
// - Supports plain text / URLs -> automatically syncs to Windows clipboard!
func (s *Server) handleShortcutUpload(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	deviceName := decodeHeaderFilename(r.Header.Get("X-Device"))
	if deviceName == "" {
		deviceName = "iPhone 快捷指令"
	}

	ct := strings.ToLower(r.Header.Get("Content-Type"))

	// Case 1: Multipart form-data upload
	if strings.HasPrefix(ct, "multipart/form-data") {
		mr, err := r.MultipartReader()
		if err != nil {
			s.logShortcut(r, http.StatusBadRequest, "Multipart 解析失败: "+err.Error())
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		var saved []transfer.TransferItem
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				s.logShortcut(r, http.StatusBadRequest, "上传流中断: "+err.Error())
				writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			fname := part.FileName()
			if fname == "" {
				// Could be a text field sent via Shortcuts Form
				raw, _ := io.ReadAll(io.LimitReader(part, 2*1024*1024))
				_ = part.Close()
				txt := strings.TrimSpace(string(raw))
				if txt != "" && (part.FormName() == "text" || part.FormName() == "clipboard") {
					s.acceptShortcutText(r, deviceName, txt)
					writeJSON(w, http.StatusOK, map[string]any{"ok": true, "kind": "text", "message": "已写入电脑剪贴板"})
					return
				}
				continue
			}

			if cfg.ConfirmReceive {
				if !s.waitShortcutConsent(r, deviceName, fname, r.ContentLength) {
					_ = part.Close()
					s.logShortcut(r, http.StatusForbidden, "电脑端拒绝或超时未确认: "+fname)
					writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "电脑端拒绝接收"})
					return
				}
			}

			finalPath, finalName, written, mimeType, serr := transfer.StreamSaveToDir(cfg.SaveDir, fname, part, part.Header.Get("Content-Type"))
			_ = part.Close()
			if serr != nil {
				s.logShortcut(r, http.StatusInternalServerError, "保存失败: "+serr.Error())
				writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": serr.Error()})
				return
			}
			saved = append(saved, s.recordShortcutFile(deviceName, finalPath, finalName, written, mimeType))
		}
		if len(saved) == 0 {
			s.logShortcut(r, http.StatusBadRequest, "快捷指令未包含文件数据")
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "未收到文件"})
			return
		}
		s.logShortcut(r, http.StatusOK, fmt.Sprintf("已接收 %d 个文件（%s）", len(saved), saved[0].Name))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": saved, "message": "发送成功: " + saved[0].Name})
		return
	}

	// Extract filename from X-Filename, query ?filename=, or Content-Disposition
	rawFilename := decodeHeaderFilename(r.Header.Get("X-Filename"))
	if rawFilename == "" {
		rawFilename = strings.TrimSpace(r.URL.Query().Get("filename"))
	}
	if rawFilename == "" {
		if cd := r.Header.Get("Content-Disposition"); cd != "" {
			if _, params, err := mime.ParseMediaType(cd); err == nil {
				rawFilename = params["filename"]
			}
		}
	}

	// Case 2: Pure text / URL shared without filename -> sync directly to Windows clipboard!
	if rawFilename == "" && strings.HasPrefix(ct, "text/plain") && r.ContentLength >= 0 && r.ContentLength < 1024*1024 {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
		if err == nil && !isBinaryPayload(raw, "") {
			txt := strings.TrimSpace(string(raw))
			if txt != "" {
				s.acceptShortcutText(r, deviceName, txt)
				writeJSON(w, http.StatusOK, map[string]any{"ok": true, "kind": "text", "message": "已复制到电脑剪贴板"})
				return
			}
		}
		if err == nil && len(raw) > 0 {
			r.Body = io.NopCloser(bytes.NewReader(raw))
		}
	}

	// Case 3: Raw binary stream ("请求体: 文件") — best mode for iOS 4K videos, HEIC, ZIP, PDF, etc.!
	if cfg.ConfirmReceive {
		displayName := rawFilename
		if displayName == "" {
			displayName = "来自快捷指令的文件"
		}
		if !s.waitShortcutConsent(r, deviceName, displayName, r.ContentLength) {
			s.logShortcut(r, http.StatusForbidden, "电脑端拒绝接收: "+displayName)
			writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "电脑端拒绝接收"})
			return
		}
	}

	finalPath, finalName, written, mimeType, err := transfer.StreamSaveToDir(cfg.SaveDir, rawFilename, r.Body, r.Header.Get("Content-Type"))
	if err != nil {
		s.logShortcut(r, http.StatusInternalServerError, "保存失败: "+err.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "保存文件失败: " + err.Error()})
		return
	}
	if written == 0 {
		s.logShortcut(r, http.StatusBadRequest, "收到了 0 字节空文件（请检查快捷指令请求体是否选了「文件」）")
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "文件为空，请在快捷指令中将请求体设为「文件」"})
		return
	}

	item := s.recordShortcutFile(deviceName, finalPath, finalName, written, mimeType)
	s.logShortcut(r, http.StatusOK, fmt.Sprintf("接收成功: %s (%s)", finalName, formatBytes(written)))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"item":    item,
		"message": fmt.Sprintf("已发送到电脑: %s (%s)", finalName, formatBytes(written)),
	})
}

func (s *Server) waitShortcutConsent(r *http.Request, deviceName, fileName string, size int64) bool {
	if size < 0 {
		size = 0
	}
	req, ch := s.store.CreatePending(deviceName, remoteIP(r), []transfer.FileMeta{{Name: fileName, Size: size}}, size)
	s.hub.BroadcastToRole("pc", transfer.Event{Type: "pending_request", Data: req})
	if s.cfgMgr.Get().NotifyOnReceive {
		winutil.ShowToast("快捷指令接收请求", fmt.Sprintf("%s 想发送「%s」— 请在电脑主界面确认", deviceName, fileName), s.iconPNG, "")
	}
	select {
	case accepted := <-ch:
		return accepted
	case <-time.After(45 * time.Second):
		s.store.CancelPending(req.ID)
		s.hub.BroadcastToRole("pc", transfer.Event{Type: "pending_resolved", Data: map[string]any{"id": req.ID, "accept": false, "timeout": true}})
		return false
	case <-r.Context().Done():
		s.store.CancelPending(req.ID)
		s.hub.BroadcastToRole("pc", transfer.Event{Type: "pending_resolved", Data: map[string]any{"id": req.ID, "accept": false, "cancelled": true}})
		return false
	}
}

func (s *Server) recordShortcutFile(deviceName, finalPath, finalName string, written int64, mimeType string) transfer.TransferItem {
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
	if s.cfgMgr.Get().NotifyOnReceive {
		winutil.ShowToast(
			fmt.Sprintf("收到来自 %s 的文件", deviceName),
			fmt.Sprintf("%s (%s)", finalName, formatBytes(written)),
			s.iconPNG,
			finalPath,
		)
	}
	return item
}

func (s *Server) acceptShortcutText(r *http.Request, deviceName, txt string) {
	cfg := s.cfgMgr.Get()
	if cfg.AutoCopyText {
		_ = clipboard.WriteText(txt)
		s.lastClipText = txt
		s.lastClipSeq = clipboard.SequenceNumber()
	}
	item := s.store.AddClipboard(transfer.ClipboardItem{
		Kind:       "text",
		Text:       txt,
		Source:     "remote",
		DeviceName: deviceName,
	})
	s.hub.Broadcast(transfer.Event{Type: "clipboard_new", Data: item})
	preview := txt
	if len([]rune(preview)) > 60 {
		preview = string([]rune(preview)[:60]) + "..."
	}
	s.logShortcut(r, http.StatusOK, "剪贴板文本已同步: "+preview)
	if cfg.NotifyOnReceive {
		winutil.ShowToast(fmt.Sprintf("快捷指令剪贴板 (%s)", deviceName), preview, s.iconPNG, "")
	}
}

func (s *Server) handleShortcutClipboardSet(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	deviceName := decodeHeaderFilename(r.Header.Get("X-Device"))
	if deviceName == "" {
		deviceName = "iPhone 快捷指令"
	}

	// Peek first 512 bytes to distinguish genuine UTF-8 text from binary photos/videos/files.
	// If the user sends a photo/file to /api/shortcut/clipboard (e.g. from iOS Shortcuts),
	// automatically save it as a real file instead of stuffing megabytes of binary into text clipboard!
	head := make([]byte, 512)
	n, readErr := io.ReadFull(r.Body, head)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		s.logShortcut(r, http.StatusBadRequest, "读取请求内容失败")
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": readErr.Error()})
		return
	}
	head = head[:n]
	if len(head) == 0 {
		s.logShortcut(r, http.StatusBadRequest, "收到空内容")
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "内容不能为空"})
		return
	}

	fullReader := io.MultiReader(bytes.NewReader(head), r.Body)
	ct := strings.ToLower(r.Header.Get("Content-Type"))

	if isBinaryPayload(head, ct) {
		rawFilename := decodeHeaderFilename(r.Header.Get("X-Filename"))
		if rawFilename == "" {
			rawFilename = strings.TrimSpace(r.URL.Query().Get("filename"))
		}
		finalPath, finalName, written, mimeType, err := transfer.StreamSaveToDir(cfg.SaveDir, rawFilename, fullReader, r.Header.Get("Content-Type"))
		if err != nil {
			s.logShortcut(r, http.StatusInternalServerError, "保存文件失败: "+err.Error())
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		item := s.recordShortcutFile(deviceName, finalPath, finalName, written, mimeType)

		// If it's a standard image under 20MB, also asynchronously copy image to Windows clipboard!
		if cfg.AutoCopyText && strings.HasPrefix(mimeType, "image/") && !strings.Contains(mimeType, "heic") && written <= 20*1024*1024 {
			go func(p string) {
				if imgBytes, rerr := os.ReadFile(p); rerr == nil {
					if clipboard.WriteImage(imgBytes) == nil {
						s.lastClipSeq = clipboard.SequenceNumber()
					}
				}
			}(finalPath)
		}

		s.logShortcut(r, http.StatusOK, fmt.Sprintf("自动识别为文件并保存: %s (%s)", finalName, formatBytes(written)))
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"item":    item,
			"message": fmt.Sprintf("已保存到电脑: %s (%s)", finalName, formatBytes(written)),
		})
		return
	}

	raw, err := io.ReadAll(io.LimitReader(fullReader, 1024*1024))
	if err != nil {
		s.logShortcut(r, http.StatusBadRequest, "读取剪贴板内容失败")
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	text := strings.TrimSpace(string(raw))
	// Also gracefully unwrap JSON {"text": "..."} if user sent JSON from Shortcuts
	if strings.HasPrefix(text, "{") {
		var obj struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &obj) == nil && strings.TrimSpace(obj.Text) != "" {
			text = obj.Text
		}
	}

	if text == "" {
		s.logShortcut(r, http.StatusBadRequest, "剪贴板文本为空")
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "文本不能为空"})
		return
	}

	s.acceptShortcutText(r, deviceName, text)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "已复制到电脑剪贴板"})
}

// isBinaryPayload reliably distinguishes images/videos/archives/binary streams from UTF-8 text.
func isBinaryPayload(head []byte, contentType string) bool {
	if strings.HasPrefix(contentType, "image/") ||
		strings.HasPrefix(contentType, "video/") ||
		strings.HasPrefix(contentType, "audio/") ||
		strings.HasPrefix(contentType, "application/pdf") ||
		strings.HasPrefix(contentType, "application/zip") {
		return true
	}
	// Check common magic bytes (JPEG, PNG, GIF, WEBP, ISO BMFF HEIC/MOV/MP4, ZIP, PDF)
	if len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF {
		return true // JPEG
	}
	if len(head) >= 4 && head[0] == 0x89 && head[1] == 'P' && head[2] == 'N' && head[3] == 'G' {
		return true // PNG
	}
	if len(head) >= 4 && (string(head[:4]) == "%PDF" || string(head[:4]) == "GIF8" || string(head[:4]) == "RIFF") {
		return true
	}
	if len(head) >= 4 && head[0] == 'P' && head[1] == 'K' && head[2] == 3 && head[3] == 4 {
		return true // ZIP
	}
	if len(head) >= 12 && string(head[4:8]) == "ftyp" {
		return true // HEIC / MOV / MP4
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return true
	}
	// Trim up to 3 trailing bytes in case a 512-byte peek split a multi-byte UTF-8 rune
	check := head
	for trim := 0; trim < 4 && len(check) > 0; trim++ {
		if utf8.Valid(check) {
			return false
		}
		check = check[:len(check)-1]
	}
	return true
}

// handleShortcutClipboardGet returns plain UTF-8 text (zero JSON parsing needed in Apple Shortcuts!).
func (s *Server) handleShortcutClipboardGet(w http.ResponseWriter, r *http.Request) {
	txt, _ := clipboard.ReadText()
	if strings.TrimSpace(txt) == "" {
		for _, c := range s.store.ListClipboard() {
			if c.Kind == "text" && strings.TrimSpace(c.Text) != "" {
				txt = c.Text
				break
			}
		}
	}
	s.logShortcut(r, http.StatusOK, fmt.Sprintf("快捷指令读取电脑剪贴板 (%d 字符)", len([]rune(txt))))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(txt))
}

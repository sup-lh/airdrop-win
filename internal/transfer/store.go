package transfer

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type TransferItem struct {
	ID          string    `json:"id"`
	Direction   string    `json:"direction"` // "in" (remote -> PC) or "out" (PC -> remote)
	Kind        string    `json:"kind"`      // "file", "image", "text"
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	MimeType    string    `json:"mimeType"`
	LocalPath   string    `json:"localPath,omitempty"`
	TextContent string    `json:"textContent,omitempty"`
	DeviceName  string    `json:"deviceName"`
	Status      string    `json:"status"` // "done", "transferring", "rejected", "failed"
	CreatedAt   time.Time `json:"createdAt"`
}

type ClipboardItem struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"` // "text" or "image"
	Text       string    `json:"text,omitempty"`
	ImagePath  string    `json:"imagePath,omitempty"`
	MimeType   string    `json:"mimeType,omitempty"`
	Source     string    `json:"source"` // "pc" or "remote"
	DeviceName string    `json:"deviceName"`
	CreatedAt  time.Time `json:"createdAt"`
}

type PendingRequest struct {
	ID         string    `json:"id"`
	DeviceName string    `json:"deviceName"`
	RemoteIP   string    `json:"remoteIp"`
	Files      []FileMeta `json:"files"`
	TotalSize  int64     `json:"totalSize"`
	CreatedAt  time.Time `json:"createdAt"`

	decisionCh chan bool
}

type FileMeta struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Type string `json:"type"`
}

type ShortcutLog struct {
	ID        string    `json:"id"`
	Method    string    `json:"method"`
	Path      string    `json:"path"`
	RemoteIP  string    `json:"remoteIp"`
	Status    int       `json:"status"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
}

type persistedState struct {
	Transfers []TransferItem  `json:"transfers"`
	Clipboard []ClipboardItem `json:"clipboard"`
}

type Store struct {
	mu           sync.RWMutex
	historyPath  string
	clipImageDir string
	transfers    []TransferItem
	clipboard    []ClipboardItem
	shortcutLogs []ShortcutLog
	pending      map[string]*PendingRequest
	approved     map[string]time.Time // pre-approved request IDs
}

func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func NewStore(dataDir string) *Store {
	clipDir := filepath.Join(dataDir, "clipboard_images")
	_ = os.MkdirAll(clipDir, 0o755)
	s := &Store{
		historyPath:  filepath.Join(dataDir, "history.json"),
		clipImageDir: clipDir,
		pending:      make(map[string]*PendingRequest),
		approved:     make(map[string]time.Time),
	}
	s.load()
	return s
}

func (s *Store) ClipImageDir() string {
	return s.clipImageDir
}

func (s *Store) load() {
	data, err := os.ReadFile(s.historyPath)
	if err != nil {
		return
	}
	var st persistedState
	if err := json.Unmarshal(data, &st); err != nil {
		return
	}
	s.transfers = st.Transfers
	// Filter out any corrupted binary text entries so history never freezes the UI
	cleanClip := make([]ClipboardItem, 0, len(st.Clipboard))
	dirty := false
	for _, c := range st.Clipboard {
		if c.Kind == "text" && (len(c.Text) > 256*1024 || looksLikeCorruptedText(c.Text)) {
			dirty = true
			continue
		}
		cleanClip = append(cleanClip, c)
	}
	s.clipboard = cleanClip
	if dirty {
		s.saveLocked()
	}
}

func looksLikeCorruptedText(s string) bool {
	if s == "" {
		return false
	}
	if strings.ContainsRune(s, 0) {
		return true
	}
	// If more than 10% of the first 256 runes are Unicode replacement characters, it was binary data
	total, bad := 0, 0
	for _, r := range s {
		total++
		if r == '\uFFFD' {
			bad++
		}
		if total >= 256 {
			break
		}
	}
	return total >= 32 && bad*10 > total
}

func (s *Store) saveLocked() {
	if len(s.transfers) > 120 {
		s.transfers = s.transfers[:120]
	}
	if len(s.clipboard) > 60 {
		s.clipboard = s.clipboard[:60]
	}
	data, err := json.MarshalIndent(persistedState{
		Transfers: s.transfers,
		Clipboard: s.clipboard,
	}, "", "  ")
	if err != nil {
		return
	}
	tmp := s.historyPath + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, s.historyPath)
	}
}

func (s *Store) AddTransfer(item TransferItem) TransferItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	if item.ID == "" {
		item.ID = NewID()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	s.transfers = append([]TransferItem{item}, s.transfers...)
	s.saveLocked()
	return item
}

func (s *Store) ListTransfers() []TransferItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]TransferItem, len(s.transfers))
	copy(out, s.transfers)
	return out
}

func (s *Store) GetTransfer(id string) (TransferItem, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.transfers {
		if t.ID == id {
			return t, true
		}
	}
	return TransferItem{}, false
}

func (s *Store) DeleteTransfer(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.transfers {
		if t.ID == id {
			s.transfers = append(s.transfers[:i], s.transfers[i+1:]...)
			s.saveLocked()
			return true
		}
	}
	return false
}

func (s *Store) ClearTransfers(direction string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if direction == "" {
		s.transfers = nil
	} else {
		var kept []TransferItem
		for _, t := range s.transfers {
			if t.Direction != direction {
				kept = append(kept, t)
			}
		}
		s.transfers = kept
	}
	s.saveLocked()
}

func (s *Store) AddClipboard(item ClipboardItem) ClipboardItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	if item.ID == "" {
		item.ID = NewID()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	if item.Kind == "text" && len(item.Text) > 256*1024 {
		item.Text = item.Text[:256*1024]
	}
	// Deduplicate consecutive identical text entry
	if item.Kind == "text" && len(s.clipboard) > 0 && s.clipboard[0].Kind == "text" && s.clipboard[0].Text == item.Text {
		s.clipboard[0].CreatedAt = item.CreatedAt
		s.clipboard[0].Source = item.Source
		s.clipboard[0].DeviceName = item.DeviceName
		s.saveLocked()
		return s.clipboard[0]
	}
	s.clipboard = append([]ClipboardItem{item}, s.clipboard...)
	s.saveLocked()
	return item
}

func (s *Store) ListClipboard() []ClipboardItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ClipboardItem, len(s.clipboard))
	copy(out, s.clipboard)
	return out
}

func (s *Store) GetClipboard(id string) (ClipboardItem, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.clipboard {
		if c.ID == id {
			return c, true
		}
	}
	return ClipboardItem{}, false
}

func (s *Store) DeleteClipboard(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.clipboard {
		if c.ID == id {
			if c.ImagePath != "" {
				_ = os.Remove(c.ImagePath)
			}
			s.clipboard = append(s.clipboard[:i], s.clipboard[i+1:]...)
			s.saveLocked()
			return true
		}
	}
	return false
}

func (s *Store) ClearClipboard() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.clipboard {
		if c.ImagePath != "" {
			_ = os.Remove(c.ImagePath)
		}
	}
	s.clipboard = nil
	s.saveLocked()
}

func (s *Store) AddShortcutLog(entry ShortcutLog) ShortcutLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry.ID == "" {
		entry.ID = NewID()
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}
	s.shortcutLogs = append([]ShortcutLog{entry}, s.shortcutLogs...)
	if len(s.shortcutLogs) > 30 {
		s.shortcutLogs = s.shortcutLogs[:30]
	}
	return entry
}

func (s *Store) ListShortcutLogs() []ShortcutLog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ShortcutLog, len(s.shortcutLogs))
	copy(out, s.shortcutLogs)
	return out
}

// CreatePending registers a receive-confirmation prompt and returns the request + decision channel.
func (s *Store) CreatePending(deviceName, remoteIP string, files []FileMeta, totalSize int64) (*PendingRequest, <-chan bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req := &PendingRequest{
		ID:         NewID(),
		DeviceName: deviceName,
		RemoteIP:   remoteIP,
		Files:      files,
		TotalSize:  totalSize,
		CreatedAt:  time.Now(),
		decisionCh: make(chan bool, 1),
	}
	s.pending[req.ID] = req
	return req, req.decisionCh
}

func (s *Store) ResolvePending(id string, accept bool) bool {
	s.mu.Lock()
	req, ok := s.pending[id]
	if ok {
		delete(s.pending, id)
		if accept {
			s.approved[id] = time.Now().Add(15 * time.Minute)
		}
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case req.decisionCh <- accept:
	default:
	}
	return true
}

func (s *Store) CancelPending(id string) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

func (s *Store) ListPending() []*PendingRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*PendingRequest, 0, len(s.pending))
	for _, p := range s.pending {
		out = append(out, p)
	}
	return out
}

func (s *Store) IsApproved(requestID string) bool {
	if requestID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Prune expired approvals
	now := time.Now()
	for k, exp := range s.approved {
		if now.After(exp) {
			delete(s.approved, k)
		}
	}
	exp, ok := s.approved[requestID]
	return ok && now.Before(exp)
}

var reservedWinNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// SanitizeFilename guarantees a safe Windows filename with zero path traversal or illegal characters.
func SanitizeFilename(raw string) string {
	// Take only the base name across both / and \
	s := strings.ReplaceAll(raw, "\\", "/")
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		s = s[idx+1:]
	}
	s = strings.TrimSpace(s)

	var b strings.Builder
	for _, r := range s {
		// Strip control chars, bidi overrides, and Windows forbidden filename chars
		if r < 0x20 || r == 0x7f || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			continue
		}
		switch r {
		case '<', '>', ':', '"', '/', '\\', '|', '?', '*':
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	s = strings.Trim(b.String(), ". ")
	if s == "" {
		s = fmt.Sprintf("file_%s", time.Now().Format("20060102_150405"))
	}

	ext := filepath.Ext(s)
	stem := strings.TrimSuffix(s, ext)
	if reservedWinNames[strings.ToUpper(stem)] {
		stem = "_" + stem
	}
	if len(stem) > 180 {
		stem = stem[:180]
	}
	return stem + ext
}

// UniquePath resolves filename collisions cleanly (e.g., photo (1).jpg) without overwriting existing files.
func UniquePath(dir, filename string) (string, string) {
	clean := SanitizeFilename(filename)
	candidate := filepath.Join(dir, clean)
	if _, err := os.Stat(candidate); os.IsNotExist(err) {
		return candidate, clean
	}
	ext := filepath.Ext(clean)
	stem := strings.TrimSuffix(clean, ext)
	for i := 1; i < 10000; i++ {
		name := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		candidate = filepath.Join(dir, name)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate, name
		}
	}
	name := fmt.Sprintf("%s_%s%s", stem, NewID(), ext)
	return filepath.Join(dir, name), name
}

// StreamSaveToDir streams an arbitrary io.Reader directly to disk via an atomic .part file
// without buffering the entire file in memory. Sniffs MIME type from the first 512 bytes.
func StreamSaveToDir(dir, rawFilename string, r io.Reader, declaredMime string) (finalPath, finalName string, written int64, detectedMime string, err error) {
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return "", "", 0, "", err
	}

	// Read first 512 bytes to sniff MIME type and infer extension if filename lacks one
	head := make([]byte, 512)
	n, readErr := io.ReadFull(r, head)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return "", "", 0, "", readErr
	}
	head = head[:n]

	detectedMime = declaredMime
	if detectedMime == "" || detectedMime == "application/octet-stream" {
		if len(head) > 0 {
			detectedMime = http.DetectContentType(head)
		} else {
			detectedMime = "application/octet-stream"
		}
	}

	// Refine extension from filename or magic bytes (critical for iOS Shortcuts raw binary uploads!)
	filename := strings.TrimSpace(rawFilename)
	inferredFromBytes := false
	if filename == "" {
		ext := guessExtension(head, detectedMime)
		filename = fmt.Sprintf("AirDrop_%s%s", time.Now().Format("20060102_150405"), ext)
		inferredFromBytes = true
	} else if filepath.Ext(filename) == "" {
		if ext := guessExtension(head, detectedMime); ext != "" {
			filename += ext
			inferredFromBytes = true
		}
	}

	if ext := filepath.Ext(filename); ext != "" {
		if byExt := mime.TypeByExtension(ext); byExt != "" {
			detectedMime = byExt
		}
	}

	partPath := filepath.Join(dir, ".airdropwin_"+NewID()+".part")
	f, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return "", "", 0, "", err
	}

	cleanup := true
	defer func() {
		if f != nil {
			_ = f.Close()
		}
		if cleanup {
			_ = os.Remove(partPath)
		}
	}()

	if len(head) > 0 {
		nw, werr := f.Write(head)
		written += int64(nw)
		if werr != nil {
			return "", "", written, detectedMime, werr
		}
	}

	if readErr == nil {
		// Copy remaining stream with a 256 KB buffer for high LAN throughput
		buf := make([]byte, 256*1024)
		nc, cerr := io.CopyBuffer(f, r, buf)
		written += nc
		if cerr != nil {
			return "", "", written, detectedMime, cerr
		}
	}

	if err = f.Close(); err != nil {
		f = nil
		return "", "", written, detectedMime, err
	}
	f = nil

	// If extension was guessed as .zip (PK\x03\x04 container), inspect central directory in <1ms
	// to accurately distinguish .docx / .xlsx / .pptx / .epub / .apk / .odt from plain .zip!
	if inferredFromBytes && strings.EqualFold(filepath.Ext(filename), ".zip") {
		if refinedExt, refinedMime := inspectZipContainer(partPath); refinedExt != "" {
			filename = strings.TrimSuffix(filename, filepath.Ext(filename)) + refinedExt
			if refinedMime != "" {
				detectedMime = refinedMime
			}
		}
	}

	finalPath, finalName = UniquePath(dir, filename)
	if err = os.Rename(partPath, finalPath); err != nil {
		return "", "", written, detectedMime, err
	}
	cleanup = false
	return finalPath, finalName, written, detectedMime, nil
}

// inspectZipContainer inspects the ZIP central directory to identify Office OpenXML (.docx/.xlsx/.pptx),
// OpenDocument, EPUB, APK, IPA, etc.
func inspectZipContainer(zipPath string) (string, string) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", ""
	}
	defer zr.Close()

	var hasWord, hasExcel, hasPPT, hasAndroid, hasEPUB, hasPages, hasKey, hasNumbers bool
	for _, f := range zr.File {
		name := f.Name
		switch {
		case name == "word/document.xml" || strings.HasPrefix(name, "word/"):
			hasWord = true
		case name == "xl/workbook.xml" || strings.HasPrefix(name, "xl/"):
			hasExcel = true
		case name == "ppt/presentation.xml" || strings.HasPrefix(name, "ppt/"):
			hasPPT = true
		case name == "AndroidManifest.xml":
			hasAndroid = true
		case name == "META-INF/container.xml":
			hasEPUB = true
		case name == "Index/Document.iwa":
			hasPages = true
		case name == "Index/Slide.iwa":
			hasKey = true
		case name == "Index/CalculationEngine.iwa":
			hasNumbers = true
		}
	}

	switch {
	case hasWord:
		return ".docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case hasExcel:
		return ".xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case hasPPT:
		return ".pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case hasAndroid:
		return ".apk", "application/vnd.android.package-archive"
	case hasEPUB:
		return ".epub", "application/epub+zip"
	case hasKey:
		return ".key", "application/x-iwork-keynote-sffkey"
	case hasNumbers:
		return ".numbers", "application/x-iwork-numbers-sffnumbers"
	case hasPages:
		return ".pages", "application/x-iwork-pages-sffpages"
	}
	return "", ""
}

func guessExtension(head []byte, mimeType string) string {
	// 1. Check explicit specific MIME types first (e.g., iOS Shortcuts sending exact Office MIME type)
	baseMime := strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	switch baseMime {
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return ".docx"
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return ".xlsx"
	case "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return ".pptx"
	case "application/msword":
		return ".doc"
	case "application/vnd.ms-excel":
		return ".xls"
	case "application/vnd.ms-powerpoint":
		return ".ppt"
	case "application/pdf":
		return ".pdf"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/heic", "image/heif":
		return ".heic"
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "audio/mpeg":
		return ".mp3"
	case "audio/mp4", "audio/x-m4a":
		return ".m4a"
	case "text/markdown":
		return ".md"
	}

	// 2. Magic byte detection (fallback when Content-Type is generic/missing)
	if len(head) >= 12 && string(head[4:8]) == "ftyp" {
		brand := strings.ToLower(string(head[8:12]))
		switch {
		case brand == "heic" || brand == "heix" || brand == "mif1" || brand == "msf1":
			return ".heic"
		case strings.HasPrefix(brand, "qt"):
			return ".mov"
		case brand == "m4a ":
			return ".m4a"
		default:
			return ".mp4"
		}
	}
	if len(head) >= 12 && string(head[0:4]) == "RIFF" {
		kind := string(head[8:12])
		if kind == "WEBP" {
			return ".webp"
		}
		if kind == "WAVE" {
			return ".wav"
		}
		if kind == "AVI " {
			return ".avi"
		}
	}
	if len(head) >= 4 && head[0] == 'P' && head[1] == 'K' && head[2] == 3 && head[3] == 4 {
		return ".zip" // refined to .docx/.xlsx/.pptx after stream completes via inspectZipContainer
	}
	if len(head) >= 6 && head[0] == 0x37 && head[1] == 0x7A && head[2] == 0xBC && head[3] == 0xAF && head[4] == 0x27 && head[5] == 0x1C {
		return ".7z"
	}
	if len(head) >= 4 && string(head[:4]) == "Rar!" {
		return ".rar"
	}
	if len(head) >= 8 && head[0] == 0xD0 && head[1] == 0xCF && head[2] == 0x11 && head[3] == 0xE0 {
		return ".doc"
	}
	if len(head) >= 4 && string(head[:4]) == "%PDF" {
		return ".pdf"
	}
	if len(head) >= 3 && string(head[:3]) == "ID3" {
		return ".mp3"
	}
	if baseMime == "application/zip" || baseMime == "application/x-zip-compressed" {
		return ".zip"
	}
	if baseMime == "text/plain" {
		return ".txt"
	}
	if exts, _ := mime.ExtensionsByType(baseMime); len(exts) > 0 {
		return exts[0]
	}
	return ""
}

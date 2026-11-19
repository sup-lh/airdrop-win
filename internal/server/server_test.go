package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"airdrop-win/internal/config"
	"airdrop-win/internal/transfer"
)

func newTestServer(t *testing.T) (*Server, *config.Manager, string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("APPDATA", tmp)

	cfgMgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	saveDir := filepath.Join(tmp, "saved")
	_, _ = cfgMgr.Update(func(c *config.Config) {
		c.SaveDir = saveDir
		c.NotifyOnReceive = false
		c.AutoCopyText = false // don't clobber developer's real clipboard during unit tests
	})

	store := transfer.NewStore(tmp)
	hub := transfer.NewHub()
	mockFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>ok</html>")},
	}
	srv := New(cfgMgr, store, hub, mockFS, tmp, "", "", nil)
	return srv, cfgMgr, saveDir
}

func TestSecurityBoundaries(t *testing.T) {
	srv, cfgMgr, _ := newTestServer(t)
	h := srv.Handler()
	tok := cfgMgr.Get().Token

	// 1. Non-loopback client MUST be blocked from /api/local/bootstrap even with valid token
	req := httptest.NewRequest("GET", "/api/local/bootstrap?token="+tok, nil)
	req.RemoteAddr = "192.168.31.99:55123"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for remote access to /api/local/bootstrap, got %d", rec.Code)
	}

	// 2. Non-loopback client without token MUST get 401 on /api/remote/bootstrap
	req = httptest.NewRequest("GET", "/api/remote/bootstrap", nil)
	req.RemoteAddr = "192.168.31.99:55123"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}

	// 3. Pair via /api/pair sets cookie and grants access
	pairBody, _ := json.Marshal(map[string]string{"token": tok})
	req = httptest.NewRequest("POST", "/api/pair", bytes.NewReader(pairBody))
	req.RemoteAddr = "192.168.31.99:55123"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /api/pair, got %d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != "airdrop_token" {
		t.Fatalf("expected airdrop_token cookie")
	}

	req = httptest.NewRequest("GET", "/api/remote/bootstrap", nil)
	req.RemoteAddr = "192.168.31.99:55123"
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with paired cookie, got %d", rec.Code)
	}
}

func TestRemoteUploadDownloadAndShortcutFlow(t *testing.T) {
	srv, cfgMgr, saveDir := newTestServer(t)
	h := srv.Handler()
	tok := cfgMgr.Get().Token

	// 1. Multipart upload from iPhone with Chinese filename
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "旅行照片_测试.png")
	payload := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, bytes.Repeat([]byte("Z"), 2048)...)
	_, _ = fw.Write(payload)
	_ = mw.Close()

	req := httptest.NewRequest("POST", "/api/remote/upload", &body)
	req.RemoteAddr = "192.168.31.88:44321"
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Token", tok)
	req.Header.Set("X-Device", "iPhone 16 Pro")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload failed: %d %s", rec.Code, rec.Body.String())
	}

	var upResp struct {
		OK    bool                    `json:"ok"`
		Items []transfer.TransferItem `json:"items"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &upResp)
	if !upResp.OK || len(upResp.Items) != 1 {
		t.Fatalf("unexpected upload response: %s", rec.Body.String())
	}
	savedPath := filepath.Join(saveDir, upResp.Items[0].Name)
	gotBytes, err := os.ReadFile(savedPath)
	if err != nil || !bytes.Equal(gotBytes, payload) {
		t.Fatalf("saved file mismatch: err=%v len=%d", err, len(gotBytes))
	}

	// 2. HTTP Range download
	req = httptest.NewRequest("GET", "/api/files/"+upResp.Items[0].ID, nil)
	req.RemoteAddr = "192.168.31.88:44321"
	req.Header.Set("X-Token", tok)
	req.Header.Set("Range", "bytes=0-15")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.Len() != 16 {
		t.Fatalf("range download failed: code=%d len=%d", rec.Code, rec.Body.Len())
	}

	// 3. Apple Shortcut raw binary ZIP upload (fixing AirDropPlus #45)
	zipPayload := append([]byte{'P', 'K', 3, 4}, bytes.Repeat([]byte{0x42}, 4096)...)
	req = httptest.NewRequest("POST", "/api/shortcut/upload?token="+tok, bytes.NewReader(zipPayload))
	req.RemoteAddr = "192.168.31.88:44322"
	req.Header.Set("X-Filename", "归档测试.zip")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("shortcut zip upload failed: %d %s", rec.Code, rec.Body.String())
	}

	// 4. Apple Shortcut clipboard POST -> GET plain text
	req = httptest.NewRequest("POST", "/api/shortcut/clipboard?token="+tok, strings.NewReader("你好 AirDropWin 2026"))
	req.RemoteAddr = "192.168.31.88:44323"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("shortcut clipboard set failed: %d %s", rec.Code, rec.Body.String())
	}

	// Verify clipboard history recorded it
	clipItems := srv.store.ListClipboard()
	if len(clipItems) == 0 || clipItems[0].Text != "你好 AirDropWin 2026" {
		t.Fatalf("expected clipboard history entry, got %+v", clipItems)
	}

	// 5. Sending raw JPEG photo accidentally to /api/shortcut/clipboard must save as .jpg file, NOT corrupt text clipboard!
	jpegPayload := append([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}, bytes.Repeat([]byte{0xAA}, 2048)...)
	req = httptest.NewRequest("POST", "/api/shortcut/clipboard?token="+tok, bytes.NewReader(jpegPayload))
	req.RemoteAddr = "192.168.31.88:44324"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("shortcut binary-to-clipboard fallback failed: %d %s", rec.Code, rec.Body.String())
	}
	transfers := srv.store.ListTransfers()
	if len(transfers) == 0 || !strings.HasSuffix(transfers[0].Name, ".jpg") {
		t.Fatalf("expected .jpg transfer saved from binary clipboard upload, got %+v", transfers)
	}
	if gotClip := srv.store.ListClipboard(); gotClip[0].Text != "你好 AirDropWin 2026" {
		t.Fatalf("binary photo polluted text clipboard! got %+v", gotClip[0])
	}
}

func TestConfirmReceiveConsentFlow(t *testing.T) {
	srv, cfgMgr, _ := newTestServer(t)
	_, _ = cfgMgr.Update(func(c *config.Config) {
		c.ConfirmReceive = true
	})
	h := srv.Handler()
	tok := cfgMgr.Get().Token

	doneCh := make(chan int, 1)
	var consentResp struct {
		Approved  bool   `json:"approved"`
		RequestID string `json:"requestId"`
	}

	// Launch remote consent request in background
	go func() {
		reqBody, _ := json.Marshal(map[string]any{
			"deviceName": "MacBook Pro",
			"totalSize":  1024,
			"files":      []map[string]any{{"name": "demo.pdf", "size": 1024}},
		})
		req := httptest.NewRequest("POST", "/api/remote/request", bytes.NewReader(reqBody))
		req.RemoteAddr = "192.168.31.77:50000"
		req.Header.Set("X-Token", tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		_ = json.Unmarshal(rec.Body.Bytes(), &consentResp)
		doneCh <- rec.Code
	}()

	// Wait briefly for pending request to register on PC side, then approve from localhost
	var pendingID string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		list := srv.store.ListPending()
		if len(list) > 0 {
			pendingID = list[0].ID
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pendingID == "" {
		t.Fatalf("pending request was not registered")
	}

	decBody := bytes.NewReader([]byte(`{"accept":true}`))
	req := httptest.NewRequest("POST", "/api/local/pending/"+pendingID+"/decision", decBody)
	req.RemoteAddr = "127.0.0.1:50001"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("decision failed: %d", rec.Code)
	}

	code := <-doneCh
	if code != http.StatusOK || !consentResp.Approved || consentResp.RequestID == "" {
		t.Fatalf("expected approved consent, got code=%d resp=%+v", code, consentResp)
	}

	// Now upload with X-Request-Id succeeds
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "demo.pdf")
	_, _ = io.WriteString(fw, "%PDF-1.7 test")
	_ = mw.Close()

	req = httptest.NewRequest("POST", "/api/remote/upload", &buf)
	req.RemoteAddr = "192.168.31.77:50000"
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Token", tok)
	req.Header.Set("X-Request-Id", consentResp.RequestID)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("approved upload failed: %d %s", rec.Code, rec.Body.String())
	}
}

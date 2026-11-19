package transfer

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"../../Windows/System32/cmd.exe", "cmd.exe"},
		{`C:\Users\lin\测试<1>:"?.jpg`, "测试_1____.jpg"},
		{"CON.txt", "_CON.txt"},
		{"nul", "_nul"},
		{"normal 照片.heic", "normal 照片.heic"},
	}
	for _, tc := range cases {
		got := SanitizeFilename(tc.in)
		if got != tc.want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestStreamSaveAndMagicSniffing(t *testing.T) {
	dir := t.TempDir()

	// 1. ZIP magic bytes without filename (simulating iOS Shortcut raw File upload)
	zipData := append([]byte{'P', 'K', 3, 4}, bytes.Repeat([]byte("A"), 1024)...)
	p1, n1, w1, _, err := StreamSaveToDir(dir, "", bytes.NewReader(zipData), "application/octet-stream")
	if err != nil {
		t.Fatalf("StreamSaveToDir zip failed: %v", err)
	}
	if !strings.HasSuffix(n1, ".zip") {
		t.Errorf("expected .zip extension, got %q", n1)
	}
	if w1 != int64(len(zipData)) {
		t.Errorf("written=%d want=%d", w1, len(zipData))
	}
	if _, err := os.Stat(p1); err != nil {
		t.Fatalf("saved file missing: %v", err)
	}

	// 2. HEIC magic bytes without extension
	heicData := append([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'h', 'e', 'i', 'c'}, bytes.Repeat([]byte{1}, 64)...)
	_, n2, _, _, err := StreamSaveToDir(dir, "IMG_0001", bytes.NewReader(heicData), "")
	if err != nil {
		t.Fatalf("StreamSaveToDir heic failed: %v", err)
	}
	if n2 != "IMG_0001.heic" {
		t.Errorf("expected IMG_0001.heic, got %q", n2)
	}

	// 3. Collision deduplication
	_, n3, _, _, err := StreamSaveToDir(dir, "IMG_0001.heic", bytes.NewReader(heicData), "")
	if err != nil {
		t.Fatalf("StreamSaveToDir dedup failed: %v", err)
	}
	if n3 != "IMG_0001 (1).heic" {
		t.Errorf("expected IMG_0001 (1).heic, got %q", n3)
	}

	// Ensure no leftover .part files
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".part") {
			t.Errorf("unexpected leftover temp file %q", filepath.Join(dir, e.Name()))
		}
	}

	// 4. Real DOCX OpenXML ZIP container without filename -> MUST be detected as .docx, NOT .zip!
	var docxBuf bytes.Buffer
	zw := zip.NewWriter(&docxBuf)
	w, _ := zw.Create("word/document.xml")
	_, _ = w.Write([]byte("<w:document/>"))
	_ = zw.Close()

	_, nDocx, _, mimeDocx, err := StreamSaveToDir(dir, "", bytes.NewReader(docxBuf.Bytes()), "application/octet-stream")
	if err != nil {
		t.Fatalf("StreamSaveToDir docx failed: %v", err)
	}
	if !strings.HasSuffix(nDocx, ".docx") {
		t.Errorf("expected .docx extension, got %q (mime=%q)", nDocx, mimeDocx)
	}

	// 5. Real XLSX OpenXML ZIP container without filename -> MUST be detected as .xlsx!
	var xlsxBuf bytes.Buffer
	zw2 := zip.NewWriter(&xlsxBuf)
	w2, _ := zw2.Create("xl/workbook.xml")
	_, _ = w2.Write([]byte("<workbook/>"))
	_ = zw2.Close()

	_, nXlsx, _, _, err := StreamSaveToDir(dir, "", bytes.NewReader(xlsxBuf.Bytes()), "application/octet-stream")
	if err != nil {
		t.Fatalf("StreamSaveToDir xlsx failed: %v", err)
	}
	if !strings.HasSuffix(nXlsx, ".xlsx") {
		t.Errorf("expected .xlsx extension, got %q", nXlsx)
	}
}

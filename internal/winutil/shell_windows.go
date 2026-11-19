package winutil

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	comdlg32 = windows.NewLazySystemDLL("comdlg32.dll")

	procShellExecuteW   = shell32.NewProc("ShellExecuteW")
	procGetOpenFileName = comdlg32.NewProc("GetOpenFileNameW")
)

const (
	swShowNormal        = 1
	ofnAllowMultiSelect = 0x00000200
	ofnExplorer         = 0x00080000
	ofnFileMustExist    = 0x00001000
	ofnNoChangeDir      = 0x00000008
)

type openFileNameW struct {
	lStructSize       uint32
	hwndOwner         uintptr
	hInstance         uintptr
	lpstrFilter       *uint16
	lpstrCustomFilter *uint16
	nMaxCustFilter    uint32
	nFilterIndex      uint32
	lpstrFile         *uint16
	nMaxFile          uint32
	lpstrFileTitle    *uint16
	nMaxFileTitle     uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	flags             uint32
	nFileOffset       uint16
	nFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        uintptr
	dwReserved        uint32
	flagsEx           uint32
}

// OpenDashboardWindow opens the PC dashboard in Edge/Chrome --app mode (clean native desktop window),
// falling back to the default system browser if neither is found.
func OpenDashboardWindow(url string) {
	candidates := []string{
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
	}
	for _, bin := range candidates {
		if _, err := os.Stat(bin); err == nil {
			cmd := exec.Command(bin, "--app="+url, "--window-size=1080,760")
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: false}
			if cmd.Start() == nil {
				return
			}
		}
	}
	_ = shellExecute("open", url, "", "")
}

// OpenPath opens a file or folder with the default Windows handler.
func OpenPath(path string) error {
	return shellExecute("open", path, "", "")
}

// RevealInExplorer opens Windows Explorer and selects the specific file.
func RevealInExplorer(path string) error {
	cmd := exec.Command("explorer.exe", "/select,"+path)
	return cmd.Start()
}

func shellExecute(verb, file, params, dir string) error {
	vPtr, _ := syscall.UTF16PtrFromString(verb)
	fPtr, _ := syscall.UTF16PtrFromString(file)
	var pPtr, dPtr *uint16
	if params != "" {
		pPtr, _ = syscall.UTF16PtrFromString(params)
	}
	if dir != "" {
		dPtr, _ = syscall.UTF16PtrFromString(dir)
	}
	r, _, err := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(vPtr)),
		uintptr(unsafe.Pointer(fPtr)),
		uintptr(unsafe.Pointer(pPtr)),
		uintptr(unsafe.Pointer(dPtr)),
		swShowNormal,
	)
	if r <= 32 {
		if err != nil && err != syscall.Errno(0) {
			return err
		}
		return fmt.Errorf("ShellExecuteW failed with code %d", r)
	}
	return nil
}

// PickFilesDialog opens the native Windows multi-file selection dialog (zero copy).
func PickFilesDialog() ([]string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	buf := make([]uint16, 65536)
	filter := utf16.Encode([]rune("所有文件 (*.*)\x00*.*\x00\x00"))
	title, _ := syscall.UTF16PtrFromString("选择要发送到手机 / Mac 的文件")

	ofn := openFileNameW{
		lStructSize: uint32(unsafe.Sizeof(openFileNameW{})),
		lpstrFilter: &filter[0],
		lpstrFile:   &buf[0],
		nMaxFile:    uint32(len(buf)),
		lpstrTitle:  title,
		flags:       ofnAllowMultiSelect | ofnExplorer | ofnFileMustExist | ofnNoChangeDir,
	}

	r, _, _ := procGetOpenFileName.Call(uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		return nil, nil // user cancelled
	}

	// Parse null-separated list:
	// Single file:  "C:\dir\file.ext\0\0"
	// Multi file:   "C:\dir\0file1.ext\0file2.ext\0\0"
	var parts []string
	start := 0
	for i := 0; i < len(buf); i++ {
		if buf[i] == 0 {
			if i == start {
				break
			}
			parts = append(parts, string(utf16.Decode(buf[start:i])))
			start = i + 1
		}
	}
	if len(parts) == 0 {
		return nil, nil
	}
	if len(parts) == 1 {
		return parts, nil
	}
	dir := parts[0]
	files := make([]string, 0, len(parts)-1)
	for _, name := range parts[1:] {
		files = append(files, filepath.Join(dir, name))
	}
	return files, nil
}

// PickFolderDialog opens a native Windows folder selection dialog via PowerShell WinForms.
func PickFolderDialog(initialDir string) (string, error) {
	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
$dlg = New-Object System.Windows.Forms.FolderBrowserDialog
$dlg.Description = "选择隔空传文件保存目录"
$dlg.SelectedPath = %s
$dlg.ShowNewFolderButton = $true
if ($dlg.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
    [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
    Write-Output $dlg.SelectedPath
}
`, psSingleQuote(initialDir))

	out, err := runHiddenPowerShell(script)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

const autostartKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
const autostartValueName = "AirDropWin"

func IsAutostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, autostartKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	val, _, err := k.GetStringValue(autostartValueName)
	return err == nil && strings.TrimSpace(val) != ""
}

func SetAutostart(enabled bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, autostartKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !enabled {
		err = k.DeleteValue(autostartValueName)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(autostartValueName, fmt.Sprintf(`"%s" --minimized`, exe))
}

func sendToLinkPath() string {
	appData := os.Getenv("APPDATA")
	return filepath.Join(appData, "Microsoft", "Windows", "SendTo", "隔空传 (AirDropWin).lnk")
}

func IsSendToEnabled() bool {
	_, err := os.Stat(sendToLinkPath())
	return err == nil
}

// SetSendTo creates or removes the Windows Explorer right-click "发送到 -> 隔空传 (AirDropWin)" shortcut.
func SetSendTo(enabled bool, iconPath string) error {
	lnk := sendToLinkPath()
	if !enabled {
		err := os.Remove(lnk)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	script := fmt.Sprintf(`
$ws = New-Object -ComObject WScript.Shell
$sc = $ws.CreateShortcut(%s)
$sc.TargetPath = %s
$sc.Arguments = '--send'
$sc.WorkingDirectory = %s
$sc.Description = '通过隔空传发送到 iPhone / Mac'
if (Test-Path %s) { $sc.IconLocation = %s }
$sc.Save()
`, psSingleQuote(lnk), psSingleQuote(exe), psSingleQuote(filepath.Dir(exe)), psSingleQuote(iconPath), psSingleQuote(iconPath+",0"))

	_, err = runHiddenPowerShell(script)
	return err
}

// CheckFirewallRule checks whether an inbound Windows Defender Firewall rule for AirDropWin exists.
func CheckFirewallRule() bool {
	cmd := exec.Command("netsh", "advfirewall", "firewall", "show", "rule", "name=AirDropWin TCP")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return len(bytes.TrimSpace(out)) > 0
}

// AddFirewallRuleElevated prompts UAC once to add inbound TCP & mDNS UDP rules for AirDropWin on all profiles (Private + Public).
func AddFirewallRuleElevated(port int) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Run elevated PowerShell to idempotently replace & add both TCP port and program rules on Profile=Any
	ps := fmt.Sprintf(
		`netsh advfirewall firewall delete rule name="AirDropWin TCP" 2>$null; `+
			`netsh advfirewall firewall delete rule name="AirDropWin App" 2>$null; `+
			`netsh advfirewall firewall add rule name="AirDropWin TCP" dir=in action=allow protocol=TCP localport=%d profile=any enable=yes; `+
			`netsh advfirewall firewall add rule name="AirDropWin App" dir=in action=allow program=%s profile=any enable=yes`,
		port, psSingleQuote(exe),
	)
	encoded := encodeUTF16LEBase64(ps)
	return shellExecute("runas", "powershell.exe", "-NoProfile -WindowStyle Hidden -EncodedCommand "+encoded, "")
}

func runHiddenPowerShell(script string) (string, error) {
	encoded := encodeUTF16LEBase64(script)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-EncodedCommand", encoded)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func encodeUTF16LEBase64(s string) string {
	u16 := utf16.Encode([]rune(s))
	b := make([]byte, len(u16)*2)
	for i, v := range u16 {
		b[i*2] = byte(v)
		b[i*2+1] = byte(v >> 8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func psSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

package winutil

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// ShowToast displays a native Windows 11 toast notification asynchronously.
// If targetPath is provided, clicking the toast or action button opens the file/folder.
func ShowToast(title, message, iconPath, targetPath string) {
	go func() {
		xmlTitle := xmlEscape(title)
		xmlMsg := xmlEscape(message)

		logoTag := ""
		if iconPath != "" {
			logoTag = fmt.Sprintf(`<image placement="appLogoOverride" hint-crop="circle" src="%s"/>`, xmlEscape("file:///"+filepath.ToSlash(iconPath)))
		}

		launchAttr := ""
		actionsTag := ""
		if targetPath != "" {
			fileURL := "file:///" + strings.ReplaceAll(url.PathEscape(filepath.ToSlash(targetPath)), "%2F", "/")
			dirURL := "file:///" + strings.ReplaceAll(url.PathEscape(filepath.ToSlash(filepath.Dir(targetPath))), "%2F", "/")
			launchAttr = fmt.Sprintf(` launch="%s" activationType="protocol"`, xmlEscape(fileURL))
			actionsTag = fmt.Sprintf(
				`<actions><action content="打开文件" arguments="%s" activationType="protocol"/><action content="打开目录" arguments="%s" activationType="protocol"/></actions>`,
				xmlEscape(fileURL), xmlEscape(dirURL),
			)
		}

		toastXML := fmt.Sprintf(
			`<toast%s><visual><binding template="ToastGeneric">%s<text>%s</text><text>%s</text></binding></visual>%s</toast>`,
			launchAttr, logoTag, xmlTitle, xmlMsg, actionsTag,
		)

		script := fmt.Sprintf(`
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] > $null
$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$xml.LoadXml(%s)
$toast = New-Object Windows.UI.Notifications.ToastNotification $xml
$appId = "{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe"
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($appId).Show($toast)
`, psSingleQuote(toastXML))

		_, _ = runHiddenPowerShell(script)
	}()
}

func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}

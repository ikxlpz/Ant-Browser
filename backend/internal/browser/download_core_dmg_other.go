//go:build !darwin

package browser

import "fmt"

func extractDMGCoreArchive(archivePath, dest string, progressCb func(int, string)) error {
	return fmt.Errorf("DMG 内核包仅支持在 macOS 上安装，当前平台为 %s", CoreExecutablePlatform())
}

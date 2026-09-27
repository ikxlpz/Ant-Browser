package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DMG 是磁盘镜像，必须挂载后复制完整应用包，不能按 ZIP/TAR 解压。
func extractDMGCoreArchive(archivePath, dest string, progressCb func(int, string)) (resultErr error) {
	archivePath, err := filepath.Abs(archivePath)
	if err != nil {
		return err
	}
	mountDir, err := os.MkdirTemp("", "ant-browser-core-dmg-*")
	if err != nil {
		return fmt.Errorf("创建 DMG 挂载目录失败: %w", err)
	}
	// 只删除空目录，卸载失败时不能递归删除仍然挂载的镜像内容。
	defer os.Remove(mountDir)
	progressCb(0, "正在挂载 DMG 内核镜像...")
	if err := runCoreDMGCommand(2*time.Minute, "/usr/bin/hdiutil", "attach", "-readonly", "-nobrowse", "-noautoopen", "-mountpoint", mountDir, archivePath); err != nil {
		return fmt.Errorf("挂载 DMG 失败: %w", err)
	}
	defer func() {
		if err := runCoreDMGCommand(30*time.Second, "/usr/bin/hdiutil", "detach", mountDir); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("卸载 DMG 失败（挂载目录 %s）: %w", mountDir, err))
		}
		if resultErr == nil {
			progressCb(100, "DMG 内核安装完成！")
		}
	}()

	entries, err := os.ReadDir(mountDir)
	if err != nil {
		return err
	}
	var bundles []string
	for _, entry := range entries {
		// 不跟随 Applications 等安装快捷链接，也不复制镜像中的装饰文件。
		if !entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".app") {
			continue
		}
		if _, _, ok := FindCoreExecutableShallow(filepath.Join(mountDir, entry.Name())); ok {
			bundles = append(bundles, entry.Name())
		}
	}
	if len(bundles) == 0 {
		return fmt.Errorf("DMG 中未找到可用的 Chromium.app 或 Google Chrome.app 内核")
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	for index, bundle := range bundles {
		progressCb(10+index*80/len(bundles), "正在复制内核应用 "+bundle+"...")
		// ditto 保留 Frameworks 符号链接、执行权限及应用包元数据。
		if err := runCoreDMGCommand(10*time.Minute, "/usr/bin/ditto", filepath.Join(mountDir, bundle), filepath.Join(dest, bundle)); err != nil {
			return fmt.Errorf("复制 DMG 内核失败: %w", err)
		}
	}
	return nil
}

func runCoreDMGCommand(timeout time.Duration, executable string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, executable, args...).CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("%s 执行超时: %w", filepath.Base(executable), ctx.Err())
	}
	if err != nil {
		return fmt.Errorf("%s: %w: %s", filepath.Base(executable), err, strings.TrimSpace(string(output)))
	}
	return nil
}

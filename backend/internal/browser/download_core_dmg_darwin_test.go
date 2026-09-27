package browser

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExtractDMGCoreArchive(t *testing.T) {
	source := t.TempDir()
	executable := filepath.Join("Chromium.app", "Contents", "MacOS", "Chromium")
	frameworks := filepath.Join("Chromium.app", "Contents", "Frameworks", "Chromium Framework.framework")
	version := filepath.Join(frameworks, "Versions", "148.0")
	for _, dir := range []string{filepath.Dir(executable), version} {
		if err := os.MkdirAll(filepath.Join(source, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, executable), []byte("browser fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, version, "Chromium Framework"), []byte("framework fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(frameworks, "Versions", "Current")
	if err := os.Symlink("148.0", filepath.Join(source, link)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/Applications", filepath.Join(source, "Applications")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".DS_Store"), []byte("decoration"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive := createTestCoreDMG(t, source)
	dest := filepath.Join(t.TempDir(), "AntChromium")
	lastProgress := -1
	if err := extractCoreArchiveAndStripRoot(archive, dest, func(progress int, _ string) {
		lastProgress = progress
	}); err != nil {
		t.Fatal(err)
	}
	if lastProgress != 100 {
		t.Fatalf("completion was not reported: %d", lastProgress)
	}
	path, _, ok := FindCoreExecutable(dest)
	if !ok || path != filepath.Join(dest, executable) {
		t.Fatalf("installed bundle is not discoverable: %q", path)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("executable permissions lost: %v", err)
	}
	if got, err := os.Readlink(filepath.Join(dest, link)); err != nil || got != "148.0" {
		t.Fatalf("framework symlink lost: %q, %v", got, err)
	}
	if data, err := os.ReadFile(filepath.Join(dest, link, "Chromium Framework")); err != nil || string(data) != "framework fixture" {
		t.Fatalf("framework symlink does not resolve: %q, %v", data, err)
	}
	entries, err := os.ReadDir(dest)
	if err != nil || len(entries) != 1 || entries[0].Name() != "Chromium.app" {
		t.Fatalf("unexpected installed files: %v, %v", entries, err)
	}
	assertCoreDMGUnmounted(t, archive)
}

func TestExtractDMGWithoutBrowser(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "README.txt"), []byte("no browser"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive := createTestCoreDMG(t, source)
	err := ExtractCoreArchiveAndStripRootForImport(archive, t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "未找到") {
		t.Fatalf("expected missing browser error: %v", err)
	}
	assertCoreDMGUnmounted(t, archive)
}

func createTestCoreDMG(t *testing.T, source string) string {
	t.Helper()
	archive := filepath.Join(t.TempDir(), "test core.dmg")
	if err := runCoreDMGCommand(time.Minute, "/usr/bin/hdiutil", "create", "-srcfolder", source, "-volname", "Test Chromium", "-format", "UDZO", archive); err != nil {
		t.Fatal(err)
	}
	return archive
}

func assertCoreDMGUnmounted(t *testing.T, archive string) {
	t.Helper()
	// 已卸载的镜像可被重命名并不充分，直接检查 hdiutil 的挂载清单。
	output, err := exec.Command("/usr/bin/hdiutil", "info").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), archive) {
		t.Fatalf("DMG is still mounted: %s", archive)
	}
}

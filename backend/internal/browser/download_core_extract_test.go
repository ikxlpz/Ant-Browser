package browser

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCoreArchiveDMGDispatch(t *testing.T) {
	url := "https://github.com/adryfish/fingerprint-chromium/releases/download/148.0.7778.215/ungoogled-chromium_148.0.7778.215-1.1_macos.dmg?download=1#asset"
	if got := coreArchiveTempPattern(url); got != "download_*.dmg" {
		t.Fatalf("DMG extension lost: %q", got)
	}
	if isTarArchivePath("browser.dmg") {
		t.Fatal("DMG must not be treated as TAR")
	}
	wantDMG := runtime.GOOS == "darwin"
	if strings.Contains(SupportedCoreArchivePattern(), "*.dmg") != wantDMG || strings.Contains(SupportedCoreArchiveDescription(), "DMG") != wantDMG {
		t.Fatal("DMG file filter must match platform support")
	}
	err := extractCoreArchiveAndStripRoot(filepath.Join(t.TempDir(), "missing.DMG"), t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "DMG") {
		t.Fatalf("expected a DMG-specific error, got %v", err)
	}
}

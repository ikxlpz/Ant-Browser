package backend

import (
	"ant-chrome/backend/internal/browser"
	"ant-chrome/backend/internal/config"
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserCoreImportDialogOptions(t *testing.T) {
	for _, goos := range []string{"darwin", "windows", "linux"} {
		t.Run(goos, func(t *testing.T) {
			options := browserCoreImportDialogOptions(goos)
			if options.Title == "" {
				t.Fatal("missing dialog title")
			}
			if goos == "darwin" {
				// 空列表在 Wails 中走允许所有文件分支，不执行会崩溃的 UTType 转换。
				if len(options.Filters) != 0 {
					t.Fatalf("macOS must bypass native extension conversion: %+v", options.Filters)
				}
				return
			}
			if len(options.Filters) != 2 || options.Filters[0].Pattern != browser.SupportedCoreArchivePattern() || options.Filters[1].Pattern != "*.*" {
				t.Fatalf("archive/all-files filters changed on %s: %+v", goos, options.Filters)
			}
		})
	}
}

func TestImportLocalBrowserCoreArchive(t *testing.T) {
	for _, valid := range []bool{true, false} {
		name := "invalid"
		if valid {
			name = "valid"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			cfg := config.DefaultConfig()
			cfg.Browser.Cores = nil
			app := NewApp(root)
			app.config = cfg
			app.browserMgr = browser.NewManager(cfg, root)
			dao := &browserCoreDAOStub{}
			app.browserMgr.CoreDAO = dao

			archive := filepath.Join(t.TempDir(), "LocalChromium.zip")
			file, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			writer := zip.NewWriter(file)
			entry := "README.txt"
			if valid {
				entry = browser.CoreExecutableCandidates()[0]
			}
			header := &zip.FileHeader{Name: "package/" + filepath.ToSlash(entry), Method: zip.Deflate}
			header.SetMode(0o755)
			content, err := writer.CreateHeader(header)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := content.Write([]byte("fixture")); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}

			core, err := app.importLocalBrowserCoreArchive(archive)
			if !valid {
				if err == nil || !strings.Contains(err.Error(), "未找到浏览器可执行文件") || core != nil {
					t.Fatalf("invalid archive must be rejected: core=%+v, err=%v", core, err)
				}
				if len(dao.list) != 0 {
					t.Fatal("invalid archive registered a core")
				}
				entries, err := os.ReadDir(filepath.Join(root, "chrome"))
				if err != nil || len(entries) != 0 {
					t.Fatalf("failed import left files behind: %v, %v", entries, err)
				}
				return
			}
			if err != nil || core == nil {
				t.Fatalf("valid archive import failed: %v", err)
			}
			if core.CoreName != "LocalChromium" || core.CorePath != filepath.Join("chrome", "LocalChromium") || len(dao.list) != 1 {
				t.Fatalf("incorrect imported core: %+v, DAO=%+v", core, dao.list)
			}
			if _, _, ok := browser.FindCoreExecutable(filepath.Join(root, core.CorePath)); !ok {
				t.Fatal("imported executable cannot be found")
			}
		})
	}
}

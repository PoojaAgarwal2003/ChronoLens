package release

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestToolchainNoticesAllowWindowsJunction(t *testing.T) {
	root := t.TempDir()
	sdk, link := filepath.Join(root, "sdk"), filepath.Join(root, "installed-go")
	if err := os.Mkdir(sdk, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdk, "LICENSE"), []byte("notice"), 0644); err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	script := fmt.Sprintf("New-Item -ItemType Junction -Path %s -Target %s -ErrorAction Stop | Out-Null", quote(link), quote(sdk))
	if out, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput(); err != nil {
		t.Fatalf("create SDK junction: %v: %s", err, out)
	}
	b, err := readToolchainNotice(link, "LICENSE")
	if err != nil || string(b) != "notice" {
		t.Fatalf("junction SDK notice: %q %v", b, err)
	}
	if _, err := ReadRegular(link, "LICENSE"); err == nil {
		t.Fatal("project-root junction checks were weakened")
	}
}

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gen2brain/beeep"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// // INSTALLATION

func installIfNeeded() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	canonical := filepath.Join(os.Getenv("LOCALAPPDATA"), "Squeezer", "squeezer.exe")
	if strings.EqualFold(self, canonical) {
		return nil // already there
	}
	if _, err := os.Stat(canonical); err == nil {
		return nil // already installed, don't overwrite
	}
	os.MkdirAll(filepath.Dir(canonical), 0755)
	data, err := os.ReadFile(self)
	if err != nil {
		return err
	}
	os.WriteFile(canonical, data, 0755)

	createStartMenuShortcut()

	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Uninstall\Squeezer`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()

	key.SetStringValue("DisplayName", "Squeezer")
	key.SetStringValue("DisplayVersion", "0.1.0")
	key.SetStringValue("DisplayIcon", canonical)
	key.SetStringValue("UninstallString", `"`+canonical+`" --uninstall`)
	key.SetDWordValue("NoModify", 1)
	key.SetDWordValue("NoRepair", 1)

	return nil
}

func doUninstall() {
	unregisterContextMenu()
	os.Remove(filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "Squeezer.lnk"))
	registry.DeleteKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Uninstall\Squeezer`)

	self, _ := os.Executable()
	dir := filepath.Dir(self)

	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("squeezer-cleanup-%d.exe", time.Now().UnixNano()))
	data, _ := os.ReadFile(self)
	os.WriteFile(tmp, data, 0755)

	cmd := exec.Command(tmp, "--cleanup", self, dir, strconv.Itoa(os.Getpid()))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Start()

	os.Exit(0)
}

func runCleanup(targetExe, targetDir, parentPIDStr string) {
	script :=
		`
		Get-Process -Name squeezer -ErrorAction SilentlyContinue |
		Stop-Process -Force;
		Get-Process -Name ffmpeg,ffprobe,exiftool,caesiumclt -ErrorAction SilentlyContinue |
		Where-Object { $_.Path -like "$env:LOCALAPPDATA\Squeezer\*" } |
		Stop-Process -Force
		`

	cmd := exec.Command("powershell", "-NoProfile", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Run()

	pid, err := strconv.Atoi(parentPIDStr)
	if err == nil {
		if p, err := os.FindProcess(pid); err == nil {
			p.Wait() // blocks until the parent process exits
		}
	}

	os.Remove(targetExe)
	os.RemoveAll(targetDir)

	self, _ := os.Executable()
	windows.MoveFileEx(
		windows.StringToUTF16Ptr(self),
		nil,
		windows.MOVEFILE_DELAY_UNTIL_REBOOT,
	)

	beeep.Notify("Uninstalled successfully", "", notifIcon)
}

func createStartMenuShortcut() error {
	exe := filepath.Join(os.Getenv("LOCALAPPDATA"), "Squeezer", "squeezer.exe")
	dir := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs")
	lnk := filepath.Join(dir, "Squeezer.lnk")

	script := fmt.Sprintf(
		`$s = (New-Object -ComObject WScript.Shell).CreateShortcut('%s');`+
			`$s.TargetPath = '%s';`+
			`$s.IconLocation = '%s,0';`+
			`$s.Save()`,
		lnk, exe, exe,
	)

	cmd := exec.Command("powershell", "-NoProfile", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}

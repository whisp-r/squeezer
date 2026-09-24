package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Microsoft/go-winio"

	"github.com/rodrigocfd/windigo/co"
	"github.com/rodrigocfd/windigo/ui"
	"github.com/rodrigocfd/windigo/win"

	_ "embed"

	_ "github.com/whisp-r/squeezer/rsrc"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var passCounter atomic.Int64

type FileKind int

const (
	KindUnknown FileKind = iota
	KindImage
	KindVideo
)

var (
	uninstall = flag.Bool("uninstall", false, "Uninstall Squeezer")
	cleanup   = flag.Bool("cleanup", false, "Cleans up the process that called it (internal)")
)

func main() {
	runtime.LockOSThread()
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [--uninstall] [files...]\n", os.Args[0])
	}

	flag.Parse()
	args := flag.Args()

	if *uninstall {
		doUninstall()
		return
	}
	if *cleanup {
		if len(args) < 3 {
			return
		}
		runCleanup(args[0], args[1], args[2])
		return
	}

	ipc := NewIPC()

	if ipc.tryBecomePrimary() == false {
		if len(args) > 0 {
			ipc.Send(args)
		} else {
			ui.NewMain(ui.OptsMain().Title("Squeezer").Size(ui.Dpi(1, 1))).Hwnd().
				MessageBox("Squeezer is already running.", "Squeezer", co.MB_ICONINFORMATION)
		}
	} else {
		go installIfNeeded()
		mainWindow(ipc, args)
	}
}

func mainWindow(ipc *IPC, initial []string) {
	currentPaths := append([]string{}, initial...)

	wnd := ui.NewMain(ui.OptsMain().
		Title("Squeezer").
		Center(true).Size(ui.Dpi(600, 520)).
		Style(co.WS_OVERLAPPEDWINDOW).
		ClassIconId(101))

	chkReg := ui.NewCheckBox(wnd, ui.OptsCheckBox().
		Text("Register context menu").
		Position(ui.Dpi(10, 365)).
		Layout(ui.LAY_HOLD_MOVE))
	chkOverwrite := ui.NewCheckBox(wnd, ui.OptsCheckBox().
		Text("Overwrite original").
		Position(ui.Dpi(10, 395)).
		Layout(ui.LAY_HOLD_MOVE))

	btnRemove := ui.NewButton(wnd, ui.OptsButton().
		Text("Remove Selected").
		Position(ui.Dpi(200, 365)).Width(140).
		Layout(ui.LAY_HOLD_MOVE))

	btnInformation := ui.NewButton(wnd, ui.OptsButton().
		Text("FFmpeg information").
		Position(ui.Dpi(380, 365)).Width(140).
		Layout(ui.LAY_HOLD_MOVE))

	btnStripMeta := ui.NewButton(wnd, ui.OptsButton().
		Text("Strip Metadata").
		Position(ui.Dpi(200, 440)).Width(140).
		Layout(ui.LAY_HOLD_MOVE))

	btnCompressVid := ui.NewButton(wnd, ui.OptsButton().
		Text("Compress Video").
		Position(ui.Dpi(380, 440)).Width(140).
		Layout(ui.LAY_HOLD_MOVE))

	listView := ui.NewListView(wnd, ui.OptsListView().
		Position(ui.Dpi(10, 10)).Size(ui.Dpi(580, 330)).
		Layout(ui.LAY_RESIZE_RESIZE).
		CtrlStyle(co.LVS_REPORT|co.LVS_SHOWSELALWAYS).
		CtrlExStyle(co.LVS_EX_FULLROWSELECT).
		Column("File", ui.DpiX(560)))

	onPath := func(p string) {
		// TODO: Add a remove duplicates buttton
		// INFO: this is fine for such small slice
		// for _, existing := range currentPaths {
		// 	if strings.EqualFold(existing, p) {
		// 		return
		// 	}
		// }
		currentPaths = append(currentPaths, p)
		listView.AddItem(p)
	}

	confirm := func(wnd *ui.Main, kept, skipped []string, verb string) bool {
		main := fmt.Sprintf("%s %d file(s)?", verb, len(kept))

		var content string

		if len(skipped) > 0 {
			content = fmt.Sprintf("Skipped (%d, wrong type):\n%s",
				len(skipped), strings.Join(skipped, "\n"))
		}

		result, _ := win.TaskDialogIndirect(&win.TASKDIALOGCONFIG{
			HwndParent:      wnd.Hwnd(),
			WindowTitle:     "Confirm",
			MainInstruction: main,
			Content:         content,
			Width:           300,
			CommonButtons:   co.TDCBF_YES | co.TDCBF_NO,
			Flags:           co.TDF_ALLOW_DIALOG_CANCELLATION,
		})

		return result == co.ID_YES
	}

	classify := func(p string) FileKind {
		ext := strings.ToLower(filepath.Ext(p))
		isImageExt := func(ext string) bool {
			switch ext {
			case
				".jpg", ".jpeg", ".png", ".webp",
				".tiff", ".gif", ".heic", ".heif",
				".avif", ".jxl", ".arw", ".orf",
				".rw2", ".pef", ".raf", ".dng",
				".cr2", ".cr3", ".nef", ".srw",
				".tif", ".jp2", ".psd", ".mpo": // exhausted list of exiftool supported formats
				return true
			}
			return false
		}
		isVideoExt := func(ext string) bool {
			switch ext {
			case ".mp4", ".mkv", ".mov", ".avi", ".webm", ".m4v", ".wmv", ".flv":
				return true
			}
			return false
		}
		if isImageExt(ext) {
			return KindImage
		} else if isVideoExt(ext) {
			return KindVideo
		} else {
			return KindUnknown
		}
	}

	wnd.On().WmCreate(func(_ ui.WmCreate) int {
		if isContextMenuRegistered() {
			chkReg.SetCheck(true)
		}
		chkOverwrite.SetCheck(true)

		for _, p := range currentPaths {
			listView.AddItem(p)
		}

		// prewarm cache
		go getFFmpeg()
		go getFFprobe()
		go getExiftool()

		go func() {
			for p := range ipc.Incoming() {
				wnd.UiThread(func() { onPath(p) })
			}
		}()

		return 0
	})

	chkReg.On().BnClicked(func() {
		if chkReg.IsChecked() {
			if err := registerContextMenu(); err != nil {
				wnd.Hwnd().MessageBox(err.Error(), "Error", co.MB_ICONERROR)
				chkReg.SetCheck(false)
			}
		} else {
			if err := unregisterContextMenu(); err != nil {
				wnd.Hwnd().MessageBox(err.Error(), "Error", co.MB_ICONERROR)
				chkReg.SetCheck(true)
			}
		}
	})

	btnRemove.On().BnClicked(func() {
		remaining := []string{}
		for i := 0; i < listView.ItemCount(); i++ {
			item := listView.Item(i)
			if !item.IsSelected() {
				remaining = append(remaining, currentPaths[i])
			}
		}
		currentPaths = remaining
		listView.DeleteAllItems()
		for _, p := range currentPaths {
			listView.AddItem(p)
		}
	})

	btnInformation.On().BnClicked(func() {
		wnd.Hwnd().SetWindowText("Squeezer - checking ffmpeg info")
		defer wnd.Hwnd().SetWindowText("Squeezer")

		ffmpeg, err := getFFmpeg()
		if err != nil {
			wnd.Hwnd().MessageBox(err.Error(), "Error", co.MB_ICONERROR)
			return
		}

		cmd := exec.Command(ffmpeg, "-version")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		out, err := cmd.Output()
		if err != nil {
			wnd.Hwnd().MessageBox(err.Error(), "Error", co.MB_ICONERROR)
			return
		}

		wnd.Hwnd().MessageBox(string(out), "FFmpeg information", co.MB_ICONINFORMATION)
	})

	btnStripMeta.On().BnClicked(func() {
		var images, skipped []string
		for _, p := range currentPaths {
			if classify(p) == KindImage {
				images = append(images, p)
			} else {
				skipped = append(skipped, p)
			}
		}
		if len(images) == 0 {
			wnd.Hwnd().MessageBox("No images in list.", "Strip Metadata", co.MB_ICONINFORMATION)
			return
		}

		if ok := confirm(wnd, images, skipped, "Strip metadata from"); ok == false {
			return
		}

		overwrite := chkOverwrite.IsChecked()
		total := len(images)
		done := make(chan struct{}, total)

		var failedMu sync.Mutex
		var failed []string

		for _, img := range images {
			go func() {
				if err := stripMetadata(img, overwrite); err != nil {
					failedMu.Lock()
					failed = append(failed, filepath.Base(img))
					failedMu.Unlock()
				}
				done <- struct{}{}
			}()
		}

		go func() {
			wnd.UiThread(func() { wnd.Hwnd().SetWindowText(fmt.Sprintf("Squeezer - Stripping metadata: 0 out of %d", total)) })
			for i := range total {
				<-done
				wnd.UiThread(func() {
					wnd.Hwnd().SetWindowText(fmt.Sprintf("Squeezer - Stripping metadata: %d out of %d", i+1, total))
				})
			}
			wnd.UiThread(func() {
				wnd.Hwnd().SetWindowText(fmt.Sprintf("Squeezer - Finished stripping metadata (%d images) at %s", total, time.Now().Format("2006-01-02 15:04:05")))
				if len(failed) > 0 {
					msg := fmt.Sprintf("Failed on %d file(s):\n%s", len(failed), strings.Join(failed, "\n"))
					wnd.Hwnd().MessageBox(msg, "Strip Metadata", co.MB_ICONWARNING)
				}
			})
		}()
	})

	btnCompressVid.On().BnClicked(func() {
		var videos, skipped []string
		for _, p := range currentPaths {
			if classify(p) == KindVideo {
				videos = append(videos, p)
			} else {
				skipped = append(skipped, p)
			}
		}
		if len(videos) == 0 {
			wnd.Hwnd().MessageBox("No videos in list.", "Compress Video", co.MB_ICONINFORMATION)
			return
		}

		if ok := confirm(wnd, videos, skipped, "Compress"); ok == false {
			return
		}

		var failedMu sync.Mutex
		var failed []string

		overwrite := chkOverwrite.IsChecked()
		total := len(videos)
		done := make(chan struct{}, total)
		sem := make(chan struct{}, max(runtime.NumCPU()-1, 1))

		for _, vid := range videos {
			go func() {
				sem <- struct{}{}
				defer func() { <-sem }()

				if err := compressToTargetSize(vid, 15, overwrite); err != nil {
					failedMu.Lock()
					failed = append(failed, filepath.Base(vid))
					failedMu.Unlock()
				}
				done <- struct{}{}
			}()
		}

		go func() {
			wnd.UiThread(func() { wnd.Hwnd().SetWindowText(fmt.Sprintf("Squeezer - Compressing videos: 0 out of %d", total)) })
			for i := range total {
				<-done
				wnd.UiThread(func() {
					wnd.Hwnd().SetWindowText(fmt.Sprintf("Squeezer - Compressing videos: %d out of %d", i+1, total))
				})
			}
			wnd.UiThread(func() {
				wnd.Hwnd().SetWindowText(fmt.Sprintf("Squeezer - Finished compressing videos (%d videos) at %s [%d failed]", total, time.Now().Format("2006-01-02 15:04:05"), len(failed)))
				if len(failed) > 0 {
					msg := fmt.Sprintf("Failed on %d file(s):\n%s", len(failed), strings.Join(failed, "\n"))
					wnd.Hwnd().MessageBox(msg, "Compress video", co.MB_ICONWARNING)
				}
			})
		}()
	})

	wnd.RunAsMain()
}

// // INSTALLATION

func installIfNeeded() {
	self, err := os.Executable()
	if err != nil {
		return
	}
	canonical := filepath.Join(os.Getenv("LOCALAPPDATA"), "Squeezer", "squeezer.exe")
	if strings.EqualFold(self, canonical) {
		return // already there
	}
	if _, err := os.Stat(canonical); err == nil {
		return // already installed, don't overwrite
	}
	os.MkdirAll(filepath.Dir(canonical), 0755)
	data, err := os.ReadFile(self)
	if err != nil {
		return
	}
	os.WriteFile(canonical, data, 0755)

	createStartMenuShortcut()

	key, _, _ := registry.CreateKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Uninstall\Squeezer`,
		registry.SET_VALUE)
	defer key.Close()
	key.SetStringValue("DisplayName", "Squeezer")
	key.SetStringValue("DisplayVersion", "0.1.0")
	key.SetStringValue("DisplayIcon", canonical)
	key.SetStringValue("UninstallString", `"`+canonical+`" --uninstall`)
	key.SetDWordValue("NoModify", 1)
	key.SetDWordValue("NoRepair", 1)
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

// // IPC LOGIC

type IPC struct {
	pipePath string
	incoming chan string
	listener net.Listener
}

func NewIPC() *IPC {
	return &IPC{
		pipePath: `\\.\pipe\SqueezerIPC`,
		incoming: make(chan string, 100),
	}
}

// tryBecomePrimary returns true if the process owns the pipe.
func (ipc *IPC) tryBecomePrimary() bool {
	l, err := winio.ListenPipe(ipc.pipePath, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;AU)",
	})
	if err != nil {
		return false
	}
	ipc.listener = l
	go ipc.acceptLoop()
	return true
}

func (ipc *IPC) acceptLoop() {
	defer ipc.listener.Close()
	for {
		conn, err := ipc.listener.Accept()
		if err != nil {
			return
		}
		go ipc.readConn(conn)
	}
}

func (ipc *IPC) readConn(conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		ipc.incoming <- sc.Text()
	}
	_ = sc.Err()
}

// Send forwards paths to the primary. Returns false if no primary exists.
func (ipc *IPC) Send(paths []string) bool {
	conn, err := winio.DialPipe(ipc.pipePath, nil)
	if err != nil {
		return false
	}
	defer conn.Close()
	for _, p := range paths {
		fmt.Fprintln(conn, p)
	}
	return true
}

// Incoming returns the channel of received paths.
func (ipc *IPC) Incoming() <-chan string {
	return ipc.incoming
}

// // IMAGE MANIPULATION

func stripMetadata(p string, overwrite bool) error {
	exiftool, err := getExiftool()
	if err != nil {
		return err
	}

	var args []string
	if overwrite {
		args = []string{"-all=", "-overwrite_original", p}
	} else {
		out := filepath.Join(filepath.Dir(p), strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))+"_stripped"+filepath.Ext(p))
		args = []string{"-all=", "-o", out, p}
	}

	cmd := exec.Command(exiftool, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}

// // VIDEO MANIPULATION

func getVideoDuration(ffprobe, path string) (float64, error) {
	cmd := exec.Command(ffprobe, "-v", "quiet", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
}

func compressToTargetSize(vid string, targetMB int, overwrite bool) error {
	ffmpeg, err := getFFmpeg()
	if err != nil {
		return err
	}
	ffprobe, err := getFFprobe()
	if err != nil {
		return err
	}

	duration, err := getVideoDuration(ffprobe, vid)
	if err != nil {
		return fmt.Errorf("probe duration: %w", err)
	}
	if duration <= 0 {
		return errors.New("invalid duration")
	}
	if info, _ := os.Stat(vid); info.Size() <= int64(targetMB)*1024*1024 {
		return errors.New("already under target size")
	}

	const audioKbps = 128
	videoKbps := max(int(float64(targetMB)*8388.608/duration)-audioKbps, 100)

	var out string
	if overwrite {
		out = vid
	} else {
		out = filepath.Join(filepath.Dir(vid), strings.TrimSuffix(filepath.Base(vid), filepath.Ext(vid))+"_squeezed"+filepath.Ext(vid))
	}

	var tempOut string
	if overwrite {
		tempOut = out
	} else {
		tempOut = vid + ".tmp" + filepath.Ext(vid)
	}

	passlog := filepath.Join(os.TempDir(), fmt.Sprintf("squeezer-pass-%d-%d", os.Getpid(), passCounter.Add(1)))
	defer os.Remove(passlog + "-0.log")
	defer os.Remove(passlog + "-0.log.mbtree")

	common := []string{
		"-y",
		"-i", vid,
		"-c:v", "libx264",
		"-preset", "faster",
		"-b:v", fmt.Sprintf("%dk", videoKbps),
		"-pix_fmt", "yuv420p",
	}

	pass1 := append(common, "-pass", "1", "-passlogfile", passlog, "-an", "-f", "null", "NUL")
	cmd1 := exec.Command(ffmpeg, pass1...)
	cmd1.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd1.Run(); err != nil {
		return fmt.Errorf("pass 1: %w", err)
	}

	pass2 := append(common, "-pass", "2", "-passlogfile", passlog, "-c:a", "aac", "-b:a", fmt.Sprintf("%dk", audioKbps), tempOut)
	cmd2 := exec.Command(ffmpeg, pass2...)
	cmd2.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd2.Run(); err != nil {
		if overwrite {
			os.Remove(tempOut)
		}
		return fmt.Errorf("pass 2: %w", err)
	}

	if overwrite {
		return os.Rename(tempOut, vid)
	}

	return nil
}

// // REGISTRY LOGIC

func registerContextMenu() error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\*\shell\Squeezer`, registry.SET_VALUE|registry.CREATE_SUB_KEY)
	if err != nil {
		return err
	}
	defer key.Close()

	if err := key.SetStringValue(``, `Open in Squeezer`); err != nil {
		return err
	}
	if err := key.SetStringValue("MultiSelectModel", "Player"); err != nil {
		return err
	}

	cmdKey, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\*\shell\Squeezer\command`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer cmdKey.Close()

	exePath := filepath.Join(os.Getenv("LOCALAPPDATA"), "Squeezer", "squeezer.exe")
	cmd := `"` + exePath + `" "%1"`

	if err := cmdKey.SetStringValue("", cmd); err != nil {
		return err
	}

	return nil
}

func unregisterContextMenu() error {
	if err := registry.DeleteKey(registry.CURRENT_USER, `Software\Classes\*\shell\Squeezer\command`); err != nil {
		return err
	}
	if err := registry.DeleteKey(registry.CURRENT_USER, `Software\Classes\*\shell\Squeezer`); err != nil {
		return err
	}
	return nil
}

func isContextMenuRegistered() bool {
	key, err := registry.OpenKey(
		registry.CURRENT_USER,
		`Software\Classes\*\shell\Squeezer`,
		registry.QUERY_VALUE,
	)
	if err != nil {
		return false
	}
	defer key.Close()
	return true
}

// // TAR/GZ HELPERS

func extractGz(file []byte, dest string) error {
	os.MkdirAll(filepath.Dir(dest), 0755)

	gr, err := gzip.NewReader(bytes.NewReader(file))
	if err != nil {
		return err
	}
	defer gr.Close()

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(f, gr); err != nil {
		os.Remove(dest)
		return err
	}

	return nil
}

func extractTarGz(data []byte, dest string) error {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gr.Close()

	os.MkdirAll(dest, 0755)

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(dest, hdr.Name)

		if hdr.Typeflag == tar.TypeDir {
			os.MkdirAll(target, 0755)
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			continue // skip symlinks, devices, etc.
		}

		os.MkdirAll(filepath.Dir(target), 0755)
		f, err := os.Create(target)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return err
		}
		f.Close()
	}
	return nil
}

// // FFMPEG EMBED LOGIC

//go:embed assets/ffmpeg.exe.gz
var ffmpegExeGz []byte

//go:embed assets/ffprobe.exe.gz
var ffprobeExeGz []byte

var (
	ffmpegMu  sync.Mutex
	ffprobeMu sync.Mutex
)

func getFFmpeg() (string, error) {
	ffmpegMu.Lock()
	defer ffmpegMu.Unlock()

	path := filepath.Join(os.Getenv("LOCALAPPDATA"), "Squeezer", "ffmpeg.exe")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}

	if err := extractGz(ffmpegExeGz, path); err != nil {
		return "", err
	}
	return path, nil
}

func getFFprobe() (string, error) {
	ffprobeMu.Lock()
	defer ffprobeMu.Unlock()

	path := filepath.Join(os.Getenv("LOCALAPPDATA"), "Squeezer", "ffprobe.exe")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}

	if err := extractGz(ffprobeExeGz, path); err != nil {
		return "", err
	}
	return path, nil
}

// // EXIFTOOL EMBED LOGIC

//go:embed assets/exiftool.tar.gz
var exiftoolTarGz []byte

var exiftoolMu sync.Mutex

func getExiftool() (string, error) {
	exiftoolMu.Lock()
	defer exiftoolMu.Unlock()

	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "Squeezer", "exiftool")
	path := filepath.Join(dir, "exiftool.exe")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}

	if err := extractTarGz(exiftoolTarGz, dir); err != nil {
		return "", err
	}
	return path, nil
}

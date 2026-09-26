package main

import (
	_ "embed"

	_ "github.com/whisp-r/squeezer/rsrc"

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
	"slices"
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
	"github.com/rodrigocfd/windigo/x/cosh"
	"github.com/rodrigocfd/windigo/x/winsh"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/gen2brain/beeep"
)

var passCounter atomic.Int64

var (
	uninstall = flag.Bool("uninstall", false, "Uninstall Squeezer")
	cleanup   = flag.Bool("cleanup", false, "Cleans up the process that called it (internal)")
)

func main() {
	runtime.LockOSThread()
	beeep.AppName = "Squeezer"

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

	if ipc.TryBecomePrimary() == false {
		if len(args) > 0 {
			ipc.Send(args)
		} else {
			ui.NewMain(ui.OptsMain().Title("Squeezer").Size(ui.Dpi(1, 1))).Hwnd().
				MessageBox("Squeezer is already running.", "Squeezer", co.MB_ICONINFORMATION)
		}
	} else {
		go installIfNeeded()
		startMainWindow(ipc, args)
	}
}

// // GUI

func startMainWindow(ipc *IPC, initialPaths []string) {
	currentPaths := []string{}

	formatSize := func(bytes int64) string {
		switch {
		case bytes >= 1<<30:
			return fmt.Sprintf("%.2f GB", float64(bytes)/(1<<30))
		case bytes >= 1<<20:
			return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
		case bytes >= 1<<10:
			return fmt.Sprintf("%.0f KB", float64(bytes)/(1<<10))
		default:
			return fmt.Sprintf("%d B", bytes)
		}
	}

	wnd := ui.NewMain(ui.OptsMain().
		Title("Squeezer").
		Center(true).Size(ui.Dpi(720, 540)).
		Style(co.WS_OVERLAPPEDWINDOW).
		ClassIconId(101))

	listView := ui.NewListView(wnd, ui.OptsListView().
		Position(ui.Dpi(10, 10)).Size(ui.Dpi(700, 330)).
		Layout(ui.LAY_RESIZE_RESIZE).
		CtrlStyle(co.LVS_REPORT|co.LVS_SHOWSELALWAYS).
		CtrlExStyle(co.LVS_EX_FULLROWSELECT).
		Column("Size", ui.DpiX(70)).
		Column("Name", ui.DpiX(220)).
		Column("Path", ui.DpiX(400)))

	// Left column — file management
	btnAddFiles := ui.NewButton(wnd, ui.OptsButton().
		Text("Add Files").
		Position(ui.Dpi(10, 360)).Width(130).
		Layout(ui.LAY_HOLD_MOVE))

	btnRemoveSel := ui.NewButton(wnd, ui.OptsButton().
		Text("Remove Selected").
		Position(ui.Dpi(10, 395)).Width(130).
		Layout(ui.LAY_HOLD_MOVE))

	btnRemoveAll := ui.NewButton(wnd, ui.OptsButton().
		Text("Remove All").
		Position(ui.Dpi(10, 430)).Width(130).
		Layout(ui.LAY_HOLD_MOVE))

	btnSort := ui.NewButton(wnd, ui.OptsButton().
		Text("Sort by Name").
		Position(ui.Dpi(10, 465)).Width(130).
		Layout(ui.LAY_HOLD_MOVE))

	// vertical separator between columns
	ui.NewStatic(wnd, ui.OptsStatic().
		Position(ui.Dpi(155, 360)).
		Size(ui.Dpi(2, 150)).
		CtrlStyle(co.SS_ETCHEDVERT).
		Layout(ui.LAY_HOLD_MOVE))

	// Right column — image row
	ui.NewStatic(wnd, ui.OptsStatic().
		Text("Image").
		Position(ui.Dpi(170, 366)).
		Layout(ui.LAY_HOLD_MOVE))

	btnStripMetaImg := ui.NewButton(wnd, ui.OptsButton().
		Text("Strip Metadata").
		Position(ui.Dpi(220, 360)).Width(120).
		Layout(ui.LAY_HOLD_MOVE))

	btnCompressImg := ui.NewButton(wnd, ui.OptsButton().
		Text("Compress").
		Position(ui.Dpi(350, 360)).Width(90).
		Layout(ui.LAY_HOLD_MOVE))

	ui.NewStatic(wnd, ui.OptsStatic().
		Text("to").
		Position(ui.Dpi(450, 366)).
		Layout(ui.LAY_HOLD_MOVE))

	cmbTargetSizeImg := ui.NewComboBox(wnd, ui.OptsComboBox().
		Position(ui.Dpi(470, 360)).Width(100).
		Layout(ui.LAY_HOLD_MOVE).
		Texts(imageSizePresets.Labels()...).
		Select(1))

	// Right column — video row
	ui.NewStatic(wnd, ui.OptsStatic().
		Text("Video").
		Position(ui.Dpi(170, 401)).
		Layout(ui.LAY_HOLD_MOVE))

	btnStripMetaVid := ui.NewButton(wnd, ui.OptsButton().
		Text("Strip Metadata").
		Position(ui.Dpi(220, 395)).Width(120).
		Layout(ui.LAY_HOLD_MOVE))

	btnCompressVid := ui.NewButton(wnd, ui.OptsButton().
		Text("Compress").
		Position(ui.Dpi(350, 395)).Width(90).
		Layout(ui.LAY_HOLD_MOVE))

	ui.NewStatic(wnd, ui.OptsStatic().
		Text("to").
		Position(ui.Dpi(450, 401)).
		Layout(ui.LAY_HOLD_MOVE))

	cmbTargetSizeVid := ui.NewComboBox(wnd, ui.OptsComboBox().
		Position(ui.Dpi(470, 395)).Width(100).
		Layout(ui.LAY_HOLD_MOVE).
		Texts(videoSizePresets.Labels()...).
		Select(2))

	ui.NewStatic(wnd, ui.OptsStatic().
		Text("Quality:").
		Position(ui.Dpi(580, 401)).
		Layout(ui.LAY_HOLD_MOVE))

	cmbQuality := ui.NewComboBox(wnd, ui.OptsComboBox().
		Position(ui.Dpi(635, 395)).Width(75).
		Layout(ui.LAY_HOLD_MOVE).
		Texts("Low", "Medium*", "High").
		Select(1))

	// stacked checkboxes
	chkOverwrite := ui.NewCheckBox(wnd, ui.OptsCheckBox().
		Text("Overwrite originals").
		Position(ui.Dpi(170, 445)).
		Layout(ui.LAY_HOLD_MOVE))

	chkReg := ui.NewCheckBox(wnd, ui.OptsCheckBox().
		Text("Right-click menu").
		Position(ui.Dpi(170, 475)).
		Layout(ui.LAY_HOLD_MOVE))

	// adding path hook
	addPathToList := func(path string) {
		if slices.Contains(currentPaths, path) == false {
			size := "?"
			info, err := os.Stat(path)
			if err != nil {
				return
			}
			size = formatSize(info.Size())

			currentPaths = append(currentPaths, path)
			listView.AddItem(size, filepath.Base(path), path)
		}
	}

	// window on-create hook
	wnd.On().WmCreate(func(_ ui.WmCreate) int {
		if isContextMenuRegistered() {
			chkReg.SetCheck(true)
		}
		chkOverwrite.SetCheck(false)

		for _, path := range initialPaths {
			addPathToList(path)
		}

		// pre-warm cache
		go getFFmpeg()
		go getFFprobe()
		go getExiftool()
		go getCaesiumclt()

		go func() {
			for path := range ipc.Incoming() {
				wnd.UiThread(func() { // Runs on every path receive
					addPathToList(path)
				})
			}
		}()

		return 0
	})

	// Helpful abstractions

	confirmDlg := func(validPaths, invalidPaths []string, sentence string) bool {
		main := fmt.Sprintf("%s %d file(s)?", sentence, len(validPaths))

		var content string

		if len(invalidPaths) > 0 {
			content += fmt.Sprintf("Skipping (%d):\n%s\n\n",
				len(invalidPaths), strings.Join(invalidPaths, "\n"))
		}
		content += fmt.Sprintf("Processing below files (%d):\n%s",
			len(validPaths), strings.Join(validPaths, "\n"))

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

	runTaskOnCurrentPaths := func(match func(fileExtSup) bool, task func(string) error) {
		var validPaths, invalidPaths []string
		for _, path := range currentPaths {
			if match(extInfo(path)) {
				validPaths = append(validPaths, path)
			} else {
				invalidPaths = append(invalidPaths, path)
			}
		}
		if len(validPaths) == 0 {
			wnd.Hwnd().MessageBox("No valid paths for the selected action\ninside the list.", "Process", co.MB_ICONINFORMATION)
			return
		}
		if ok := confirmDlg(validPaths, invalidPaths, "Run process?"); ok == false {
			return
		}

		wnd.Hwnd().EnableWindow(false)

		total := len(validPaths)
		done := make(chan struct{}, len(validPaths))
		sem := make(chan struct{}, max(runtime.NumCPU()-1, 1))
		var failedMu sync.Mutex
		var failed []string

		// Run task inside goroutine with semaphores
		for _, path := range validPaths {
			go func() {
				sem <- struct{}{}
				defer func() { <-sem }()
				defer func() { done <- struct{}{} }()

				if err := task(path); err != nil {
					failedMu.Lock()
					failed = append(failed, fmt.Sprintf("%s: %v", filepath.Base(path), err))
					failedMu.Unlock()
				}
			}()
		}

		// UI updates
		go func() {
			defer wnd.UiThread(func() { wnd.Hwnd().EnableWindow(true) })
			wnd.UiThread(func() { wnd.Hwnd().SetWindowText(fmt.Sprintf("Squeezer - Processing: 0 out of %d", total)) })
			for i := range total {
				<-done
				wnd.UiThread(func() {
					wnd.Hwnd().SetWindowText(fmt.Sprintf("Squeezer - Processing: %d out of %d", i+1, total))
				})
			}
			wnd.UiThread(func() {
				wnd.Hwnd().SetWindowText(fmt.Sprintf("Squeezer - Finished processing (%d files) at %s", total, time.Now().Format("2006-01-02 15:04:05")))

				if len(failed) > 0 {
					msg := fmt.Sprintf("Failed on %d file(s):\n%s", len(failed), strings.Join(failed, "\n"))
					wnd.Hwnd().MessageBox(msg, "Process", co.MB_ICONWARNING)
				} else {
					// beeep spawns a helper process synchronously, which
					// blocks the UI thread long enough for Windows to kill it as hung
					go beeep.Notify("Finished processing", fmt.Sprintf("%d files at %s", total, time.Now().Format("2006-01-02 15:04:05")), notifIcon)
				}

			})
		}()
	}

	// Events

	chkReg.On().BnClicked(func() {
		chkReg.Hwnd().EnableWindow(false)
		defer chkReg.Hwnd().EnableWindow(true)

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

	btnAddFiles.On().BnClicked(func() {
		// https://github.com/rodrigocfd/windigo#component-object-model-com
		// This function is slight modification from the windigo example from the github README
		// Modified to handle multiple files
		_, _ = win.CoInitializeEx(co.COINIT_APARTMENTTHREADED | co.COINIT_DISABLE_OLE1DDE)
		defer win.CoUninitialize()

		releaser := win.NewOleReleaser() // will release all COM objects created here
		defer releaser.Release()

		var fod *winsh.IFileOpenDialog
		_ = win.CoCreateInstance(
			releaser,
			&cosh.CLSID_FileOpenDialog,
			nil,
			co.CLSCTX_INPROC_SERVER,
			&fod,
		)

		defOpts, _ := fod.GetOptions()
		_ = fod.SetOptions(defOpts | cosh.FOS_FORCEFILESYSTEM | cosh.FOS_FILEMUSTEXIST | cosh.FOS_ALLOWMULTISELECT)

		_ = fod.SetFileTypes([]winsh.COMDLG_FILTERSPEC{
			{Name: "Media files", Spec: "*.jpg;*.jpeg;*.png;*.mp4;*.mkv"},
			{Name: "All files", Spec: "*.*"},
		})
		_ = fod.SetFileTypeIndex(1)

		if ok, _ := fod.Show(wnd.Hwnd()); ok { // in real applications, pass the parent HWND
			items, _ := fod.GetResults(releaser)
			count, _ := items.GetCount()
			for i := range count {
				item, _ := items.GetItemAt(releaser, i)
				p, _ := item.GetDisplayName(cosh.SIGDN_FILESYSPATH)
				addPathToList(p)
			}
		}
	})

	btnRemoveSel.On().BnClicked(func() {
		var kept []string
		for i := range listView.ItemCount() {
			if !listView.Item(i).IsSelected() {
				kept = append(kept, currentPaths[i])
			}
		}
		currentPaths = kept
		listView.DeleteSelectedItems()
	})

	btnRemoveAll.On().BnClicked(func() {
		currentPaths = nil
		listView.DeleteAllItems()
	})

	btnSort.On().BnClicked(func() {
		sortedCurPaths := slices.Clone(currentPaths)
		slices.SortFunc(sortedCurPaths, func(a, b string) int {
			return strings.Compare(
				strings.ToLower(filepath.Base(a)),
				strings.ToLower(filepath.Base(b)))
		})

		currentPaths = nil
		listView.DeleteAllItems()

		for _, path := range sortedCurPaths {
			addPathToList(path)
		}
	})

	btnStripMetaImg.On().BnClicked(func() {
		ow := chkOverwrite.IsChecked()
		runTaskOnCurrentPaths(
			func(fes fileExtSup) bool { return fes.Image && fes.Strip },
			func(p string) error { return stripImageMetadata(p, ow) },
		)
	})

	btnCompressImg.On().BnClicked(func() {
		i := cmbTargetSizeImg.SelectedIndex()
		if i < 0 || i >= len(imageSizePresets) {
			wnd.Hwnd().MessageBox("Select a target size.", "Compress", co.MB_ICONERROR)
			return
		}
		tb := imageSizePresets[i].bytes

		ow := chkOverwrite.IsChecked()
		runTaskOnCurrentPaths(
			func(fes fileExtSup) bool { return fes.Image && fes.Compress },
			func(p string) error { return compressImageToTargetSize(p, tb, ow) },
		)
	})

	btnStripMetaVid.On().BnClicked(func() {
		ow := chkOverwrite.IsChecked()
		runTaskOnCurrentPaths(
			func(fes fileExtSup) bool { return fes.Video && fes.Strip },
			func(p string) error { return stripVideoMetadata(p, ow) },
		)
	})

	btnCompressVid.On().BnClicked(func() {
		i := cmbTargetSizeVid.SelectedIndex()
		if i < 0 || i >= len(videoSizePresets) {
			wnd.Hwnd().MessageBox("Select a target size.", "Compress", co.MB_ICONERROR)
			return
		}
		tb := videoSizePresets[i].bytes

		q := cmbQuality.CurrentText()
		ow := chkOverwrite.IsChecked()
		runTaskOnCurrentPaths(
			func(fes fileExtSup) bool { return fes.Video && fes.Compress },
			func(p string) error { return compressVideoToTargetSize(p, tb, q, ow) },
		)
	})

	wnd.RunAsMain()
}

// // SUPPORTED EXTENSIONS

type fileExtSup struct {
	Image    bool
	Video    bool
	Strip    bool
	Compress bool
}

var extTable = map[string]fileExtSup{
	// caesiumclt: compression support
	// exiftool: stripping support
	".jpg":  {Image: true, Strip: true, Compress: true},
	".jpeg": {Image: true, Strip: true, Compress: true},
	".png":  {Image: true, Strip: true, Compress: true},
	".webp": {Image: true, Strip: true, Compress: true},
	".tiff": {Image: true, Strip: true},
	".gif":  {Image: true, Strip: true},
	".heic": {Image: true, Strip: true},
	".heif": {Image: true, Strip: true},
	".avif": {Image: true, Strip: true},
	".jxl":  {Image: true, Strip: true},
	".arw":  {Image: true, Strip: true},
	".orf":  {Image: true, Strip: true},
	".rw2":  {Image: true, Strip: true},
	".pef":  {Image: true, Strip: true},
	".raf":  {Image: true, Strip: true},
	".dng":  {Image: true, Strip: true},
	".cr2":  {Image: true, Strip: true},
	".cr3":  {Image: true, Strip: true},
	".nef":  {Image: true, Strip: true},
	".srw":  {Image: true, Strip: true},
	".tif":  {Image: true, Strip: true},
	".jp2":  {Image: true, Strip: true},
	".psd":  {Image: true, Strip: true},
	".mpo":  {Image: true, Strip: true},

	// ffmpeg: compression support
	// ffmpeg: stripping support
	".mp4":  {Video: true, Strip: true, Compress: true},
	".mkv":  {Video: true, Strip: true, Compress: true},
	".mov":  {Video: true, Strip: true, Compress: true},
	".avi":  {Video: true, Strip: true, Compress: true},
	".webm": {Video: true, Strip: true, Compress: true},
	".wmv":  {Video: true, Strip: true, Compress: true},
	".m4v":  {Video: true, Compress: true},
	".flv":  {Video: true, Compress: true},
}

func extInfo(path string) fileExtSup {
	return extTable[strings.ToLower(filepath.Ext(path))]
}

// // PRESET SIZES

type sizePresets []sizePreset
type sizePreset struct {
	label string
	bytes uint64
}

func (p sizePresets) Labels() []string {
	out := make([]string, len(p))
	for i, v := range p {
		out[i] = v.label
	}
	return out
}

var videoSizePresets = sizePresets{
	{"2 MB", 2 << 20},
	{"4 MB", 4 << 20},
	{"9 MB", 9 << 20},
	{"14 MB", 14 << 20},
	{"20 MB", 20 << 20},
	{"50 MB", 50 << 20},
	{"100 MB", 100 << 20},
	{"250 MB", 250 << 20},
}

var imageSizePresets = sizePresets{
	{"10 KB", 10 << 10},
	{"25 KB", 25 << 10},
	{"50 KB", 50 << 10},
	{"100 KB", 100 << 10},
	{"250 KB", 250 << 10},
	{"500 KB", 500 << 10},
	{"1 MB", 1 << 20},
	{"2 MB", 2 << 20},
	{"5 MB", 5 << 20},
}

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

// // WINDOWS IPC

type IPC struct {
	pipePath string
	incoming chan string
	listener net.Listener
}

func NewIPC() *IPC {
	return &IPC{
		pipePath: `\\.\pipe\SqueezerIPC`,
		incoming: make(chan string, 100),
		listener: nil,
	}
}

// TryBecomePrimary returns true if the process owns the pipe.
func (ipc *IPC) TryBecomePrimary() bool {
	listener, err := winio.ListenPipe(ipc.pipePath, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;AU)"})
	if err != nil {
		return false
	}

	ipc.listener = listener
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

// Send forwards paths to the primary.
func (ipc *IPC) Send(paths []string) error {
	conn, err := winio.DialPipe(ipc.pipePath, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	for _, p := range paths {
		fmt.Fprintln(conn, p)
	}
	return nil
}

// Incoming returns the channel of received paths.
func (ipc *IPC) Incoming() <-chan string {
	return ipc.incoming
}

// // IMAGE MANIPULATION

func stripImageMetadata(path string, overwrite bool) error {
	exiftool, err := getExiftool()
	if err != nil {
		return err
	}

	// beucase exiftool sometimes leaves files due to how windows handles events (should be fixed with EnableWindow(false)) kept for safety
	defer os.Remove(filepath.Join(filepath.Dir(path), filepath.Base(path)+"_exiftool_tmp"))

	var args []string
	if overwrite {
		args = []string{"-all=", "-overwrite_original", path}
	} else {
		out := filepath.Join(filepath.Dir(path), strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+"_stripped"+filepath.Ext(path))
		args = []string{"-all=", "-o", out, path}
	}

	cmd := exec.Command(exiftool, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}

func compressImageToTargetSize(path string, targetBytes uint64, overwrite bool) error {
	caesiumclt, err := getCaesiumclt()
	if err != nil {
		return err
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	if uint64(info.Size()) <= targetBytes {
		return errors.New("already under target size")
	}

	// 	# Compress with suffix to avoid overwriting originals
	// $ caesiumclt -q 85 --suffix _compressed --same-folder-as-input image.jpg

	// # Target a specific maximum file size (500KB)
	// $ caesiumclt --max-size 512000 -o output/ large-image.jpg

	var args []string
	if overwrite {
		args = []string{"--max-size", fmt.Sprintf("%d", targetBytes), "--same-folder-as-input", path}
	} else {
		args = []string{"--max-size", fmt.Sprintf("%d", targetBytes), "--suffix", "_compressed", "--same-folder-as-input", path}
	}

	cmd := exec.Command(caesiumclt, args...)
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

func stripVideoMetadata(path string, overwrite bool) error {
	ffmpeg, err := getFFmpeg()
	if err != nil {
		return err
	}

	var out string
	if overwrite {
		out = path
	} else {
		out = filepath.Join(filepath.Dir(path), strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+"_stripped"+filepath.Ext(path))
	}

	var tempOut string
	if overwrite {
		tempOut = path + ".tmp" + filepath.Ext(path)
	} else {
		tempOut = out
	}

	args := []string{
		"-y",
		"-fflags", "+bitexact",
		"-i", path,
		"-map_metadata", "-1",
		"-map_chapters", "-1",
		"-c", "copy",
		"-flags", "+bitexact",
		tempOut,
	}

	cmd := exec.Command(ffmpeg, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Run(); err != nil {
		if overwrite {
			os.Remove(tempOut)
		}
		return fmt.Errorf("strip metadata: %w", err)
	}

	if overwrite {
		return os.Rename(tempOut, path)
	}

	return nil
}

func compressVideoToTargetSize(path string, targetBytes uint64, quality string, overwrite bool) error {
	ffmpeg, err := getFFmpeg()
	if err != nil {
		return err
	}
	ffprobe, err := getFFprobe()
	if err != nil {
		return err
	}

	duration, err := getVideoDuration(ffprobe, path)
	if err != nil {
		return fmt.Errorf("probe duration: %w", err)
	}
	if duration <= 0 {
		return errors.New("invalid duration")
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	if uint64(info.Size()) <= targetBytes {
		return errors.New("already under target size")
	}

	const audioKbps = 128
	videoKbps := max(int(float64(targetBytes)*8/1000/duration)-audioKbps, 100)

	var out string
	if overwrite {
		out = path
	} else {
		out = filepath.Join(filepath.Dir(path), strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+"_squeezed"+filepath.Ext(path))
	}

	var tempOut string
	if overwrite {
		tempOut = path + ".tmp" + filepath.Ext(path)
	} else {
		tempOut = out
	}

	passlog := filepath.Join(os.TempDir(), fmt.Sprintf("squeezer-pass-%d-%d", os.Getpid(), passCounter.Add(1)))
	defer os.Remove(passlog + "-0.log")
	defer os.Remove(passlog + "-0.log.mbtree")

	var preset string
	switch quality {
	case "Low":
		preset = "veryfast"
	case "Medium*":
		preset = "medium"
	case "High":
		preset = "veryslow"
	}

	common := []string{
		"-y",
		"-i", path,
		"-vf", "scale=-2:1080",
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		"-preset", preset,
		"-b:v", fmt.Sprintf("%dk", videoKbps),
	}

	// Two-Pass logic

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
		return os.Rename(tempOut, path)
	}

	return nil
}

// // WINDOWS REGISTRY

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

// // TAR/GZ HELPERS (vibecoded)

func extractGz(file []byte, dest string) error {
	os.MkdirAll(filepath.Dir(dest), 0755)

	gr, err := gzip.NewReader(bytes.NewReader(file))
	if err != nil {
		return err
	}
	defer gr.Close()

	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}

	if _, err := io.Copy(f, gr); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}

	return os.Rename(tmp, dest)
}

func extractTarGz(data []byte, dest string) error {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gr.Close()

	parent := filepath.Dir(dest)
	os.MkdirAll(parent, 0755)

	tmpDir := dest + ".tmp"
	os.RemoveAll(tmpDir) // in case of prior crash
	os.MkdirAll(tmpDir, 0755)

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			os.RemoveAll(tmpDir)
			return err
		}

		target := filepath.Join(tmpDir, hdr.Name)

		if hdr.Typeflag == tar.TypeDir {
			os.MkdirAll(target, 0755)
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		os.MkdirAll(filepath.Dir(target), 0755)
		f, err := os.Create(target)
		if err != nil {
			os.RemoveAll(tmpDir)
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			os.RemoveAll(tmpDir)
			return err
		}
		f.Close()
	}

	os.RemoveAll(dest) // remove any partial from a prior crash
	return os.Rename(tmpDir, dest)
}

// // FFMPEG EMBED

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
	if _, err := os.Stat(path); err != nil {
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
	if _, err := os.Stat(path); err != nil {
		return "", err
	}

	return path, nil
}

// // EXIFTOOL EMBED

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
	if _, err := os.Stat(path); err != nil {
		return "", err
	}

	return path, nil
}

// // CAESIUM-CLT EMBED

//go:embed assets/caesiumclt.exe.gz
var caesiumcltExeGz []byte

var caesiumcltMu sync.Mutex

func getCaesiumclt() (string, error) {
	caesiumcltMu.Lock()
	defer caesiumcltMu.Unlock()

	path := filepath.Join(os.Getenv("LOCALAPPDATA"), "Squeezer", "caesiumclt.exe")

	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := extractGz(caesiumcltExeGz, path); err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		return "", err
	}

	return path, nil
}

// // ICON EMBED

//go:embed rsrc/gopher.png
var notifIcon []byte

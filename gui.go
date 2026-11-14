package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/gen2brain/beeep"
	"github.com/rodrigocfd/windigo/co"
	"github.com/rodrigocfd/windigo/ui"
	"github.com/rodrigocfd/windigo/win"
	"github.com/rodrigocfd/windigo/x/cosh"
	"github.com/rodrigocfd/windigo/x/winsh"
)

// SUPPORTED EXTENSIONS

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

// PRESET SIZES

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

// MAIN WINDOW

type MainWindow struct {
	main *ui.Main

	lstView    *ui.ListView
	lstPaths   []string
	lstSortCol int
	lstSortAsc bool

	btnAddFiles  *ui.Button
	btnRemoveSel *ui.Button
	btnRemoveAll *ui.Button
	// btnSort      *ui.Button

	verticalSep *ui.Static

	lblImage         *ui.Static
	btnStripMetaImg  *ui.Button
	btnCompressImg   *ui.Button
	lblImageTo       *ui.Static
	cmbTargetSizeImg *ui.ComboBox

	lblVideo         *ui.Static
	btnStripMetaVid  *ui.Button
	btnCompressVid   *ui.Button
	lblVideoTo       *ui.Static
	cmbTargetSizeVid *ui.ComboBox
	lblVideoQuality  *ui.Static
	cmbQuality       *ui.ComboBox

	chkOverwrite *ui.CheckBox
	chkRegistry  *ui.CheckBox
}

func (me *MainWindow) AddPath(path string) {
	if !slices.Contains(me.lstPaths, path) {
		me.lstPaths = append(me.lstPaths, path)
	}
}

func (me *MainWindow) SyncLstViewPaths() {
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

	me.lstView.DeleteAllItems()

	for _, path := range me.lstPaths {
		size := "?"
		info, err := os.Stat(path)
		if err != nil {
			return
		}
		size = formatSize(info.Size())
		me.lstView.AddItem(size, filepath.Base(path), path)
	}
}

func (me *MainWindow) Events() {
	preWarmCache := func() {
		go getFFmpeg()
		go getFFprobe()
		go getExiftool()
		go getCaesiumclt()
	}

	me.main.On().WmCreate(func(_ ui.WmCreate) int {
		if isContextMenuRegistered() {
			me.chkRegistry.SetCheck(true)
		}
		me.chkOverwrite.SetCheck(false)

		for _, path := range flag.Args() {
			me.AddPath(path)
		}

		preWarmCache()

		return 0
	})

	me.lstSortCol = -1
	me.lstSortAsc = true
	me.lstView.On().LvnColumnClick(func(p *win.NMLISTVIEW) {
		setSortArrow := func(col int, ascending bool) {
			for i := range me.lstView.Cols() {
				var fmtFlag co.HDF = co.HDF_LEFT
				if i == col {
					if ascending {
						fmtFlag = co.HDF_SORTUP
					} else {
						fmtFlag = co.HDF_SORTDOWN
					}
				}

				lvc := win.LVCOLUMN{
					Mask: co.LVCF_FMT,
					Fmt:  co.LVCFMT_C(fmtFlag),
				}

				me.lstView.Hwnd().SendMessage(
					co.LVM_SETCOLUMN,
					win.WPARAM(i),
					win.LPARAM(unsafe.Pointer(&lvc)),
				)
			}
		}
		col := int(p.ISubItem)

		if me.lstSortCol == col {
			me.lstSortAsc = !me.lstSortAsc
		} else {
			me.lstSortCol = col
			me.lstSortAsc = true
		}

		slices.SortFunc(me.lstPaths, func(a, b string) int {
			var cmp int
			switch col {
			case 0: // size
				ai, _ := os.Stat(a)
				bi, _ := os.Stat(b)
				cmp = int(ai.Size() - bi.Size())
			case 1: // name
				cmp = strings.Compare(strings.ToLower(filepath.Base(a)), strings.ToLower(filepath.Base(b)))
			case 2: // path
				cmp = strings.Compare(strings.ToLower(a), strings.ToLower(b))
			}
			if !me.lstSortAsc {
				cmp = -cmp
			}
			return cmp
		})

		me.SyncLstViewPaths()
		setSortArrow(col, me.lstSortAsc)
	})

	me.chkRegistry.On().BnClicked(func() {
		me.chkRegistry.Hwnd().EnableWindow(false)
		defer me.chkRegistry.Hwnd().EnableWindow(true)

		if me.chkRegistry.IsChecked() {
			if err := registerContextMenu(); err != nil {
				me.main.Hwnd().MessageBox(err.Error(), "Error", co.MB_ICONERROR)
				me.chkRegistry.SetCheck(false)
			}
		} else {
			if err := unregisterContextMenu(); err != nil {
				me.main.Hwnd().MessageBox(err.Error(), "Error", co.MB_ICONERROR)
				me.chkRegistry.SetCheck(true)
			}
		}
	})

	me.btnAddFiles.On().BnClicked(func() {
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

		if ok, _ := fod.Show(me.main.Hwnd()); ok { // in real applications, pass the parent HWND
			items, _ := fod.GetResults(releaser)
			count, _ := items.GetCount()
			for i := range count {
				item, _ := items.GetItemAt(releaser, i)
				p, _ := item.GetDisplayName(cosh.SIGDN_FILESYSPATH)
				me.AddPath(p)
			}
			me.SyncLstViewPaths()
		}
	})

	me.btnRemoveSel.On().BnClicked(func() {
		var kept []string
		for i := range me.lstView.ItemCount() {
			if !me.lstView.Item(i).IsSelected() {
				kept = append(kept, me.lstPaths[i])
			}
		}
		me.lstPaths = kept
		me.SyncLstViewPaths()
	})

	me.btnRemoveAll.On().BnClicked(func() {
		me.lstPaths = nil
		me.SyncLstViewPaths()
	})

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
			HwndParent:      me.main.Hwnd(),
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
		for _, path := range me.lstPaths {
			if match(extInfo(path)) {
				validPaths = append(validPaths, path)
			} else {
				invalidPaths = append(invalidPaths, path)
			}
		}
		if len(validPaths) == 0 {
			me.main.Hwnd().MessageBox("No valid paths for the selected action\ninside the list.", "Process", co.MB_ICONINFORMATION)
			return
		}
		if ok := confirmDlg(validPaths, invalidPaths, "Run process?"); ok == false {
			return
		}

		me.main.Hwnd().EnableWindow(false)

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
			defer me.main.UiThread(func() { me.main.Hwnd().EnableWindow(true) })
			me.main.UiThread(func() { me.main.Hwnd().SetWindowText(fmt.Sprintf("Squeezer - Processing: 0 out of %d", total)) })
			for i := range total {
				<-done
				me.main.UiThread(func() {
					me.main.Hwnd().SetWindowText(fmt.Sprintf("Squeezer - Processing: %d out of %d", i+1, total))
				})
			}
			me.main.UiThread(func() {
				me.main.Hwnd().SetWindowText(fmt.Sprintf("Squeezer - Finished processing (%d files) at %s", total, time.Now().Format("2006-01-02 15:04:05")))

				if len(failed) > 0 {
					msg := fmt.Sprintf("Failed on %d file(s):\n%s", len(failed), strings.Join(failed, "\n"))
					me.main.Hwnd().MessageBox(msg, "Process", co.MB_ICONWARNING)
				} else {
					// beeep spawns a helper process synchronously, which
					// blocks the UI thread long enough for Windows to kill it as hung
					go beeep.Notify("Finished processing", fmt.Sprintf("%d files at %s", total, time.Now().Format("2006-01-02 15:04:05")), notifIcon)
				}

			})
		}()
	}

	me.btnStripMetaImg.On().BnClicked(func() {
		ow := me.chkOverwrite.IsChecked()
		runTaskOnCurrentPaths(
			func(fes fileExtSup) bool { return fes.Image && fes.Strip },
			func(p string) error { return stripImageMetadata(p, ow) },
		)
	})

	me.btnCompressImg.On().BnClicked(func() {
		i := me.cmbTargetSizeImg.SelectedIndex()
		tb := imageSizePresets[i].bytes

		ow := me.chkOverwrite.IsChecked()
		runTaskOnCurrentPaths(
			func(fes fileExtSup) bool { return fes.Image && fes.Compress },
			func(p string) error { return compressImageToTargetSize(p, tb, ow) },
		)
	})

	me.btnStripMetaVid.On().BnClicked(func() {
		ow := me.chkOverwrite.IsChecked()
		runTaskOnCurrentPaths(
			func(fes fileExtSup) bool { return fes.Video && fes.Strip },
			func(p string) error { return stripVideoMetadata(p, ow) },
		)
	})

	me.btnCompressVid.On().BnClicked(func() {
		i := me.cmbTargetSizeVid.SelectedIndex()
		tb := videoSizePresets[i].bytes
		q := me.cmbQuality.CurrentText()

		ow := me.chkOverwrite.IsChecked()
		runTaskOnCurrentPaths(
			func(fes fileExtSup) bool { return fes.Video && fes.Compress },
			func(p string) error { return compressVideoToTargetSize(p, tb, q, ow) },
		)
	})
}

func (me *MainWindow) OnIncomingIPC(ipc *IPC) {
	go func() {
		for path := range ipc.Incoming() {
			me.AddPath(path)
			me.main.UiThread(func() {
				me.SyncLstViewPaths()
			})
		}
	}()
}

func NewMainWindow() *MainWindow {
	wnd := ui.NewMain(ui.OptsMain().
		Title("Squeezer").
		Center(true).Size(ui.Dpi(720, 540)).
		Style(co.WS_OVERLAPPEDWINDOW).
		ClassIconId(101))

	lstView := ui.NewListView(wnd, ui.OptsListView().
		Position(ui.Dpi(10, 10)).Size(ui.Dpi(700, 330)).
		Layout(ui.LAY_RESIZE_RESIZE).
		CtrlStyle(co.LVS_REPORT|co.LVS_SHOWSELALWAYS).
		CtrlExStyle(co.LVS_EX_FULLROWSELECT).
		Column("Size", ui.DpiX(70)).
		Column("Name", ui.DpiX(220)).
		Column("Path", ui.DpiX(400)))

	var lstPath []string = nil
	lstSortCol := -1
	lstSortAsc := true

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

	// btnSort := ui.NewButton(wnd, ui.OptsButton().
	// 	Text("Sort by Name").
	// 	Position(ui.Dpi(10, 465)).Width(130).
	// 	Layout(ui.LAY_HOLD_MOVE))

	// vertical separator between columns
	verticalSep := ui.NewStatic(wnd, ui.OptsStatic().
		Position(ui.Dpi(155, 360)).
		Size(ui.Dpi(2, 150)).
		CtrlStyle(co.SS_ETCHEDVERT).
		Layout(ui.LAY_HOLD_MOVE))

	// Right column — image row
	lblImage := ui.NewStatic(wnd, ui.OptsStatic().
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

	lblImageTo := ui.NewStatic(wnd, ui.OptsStatic().
		Text("to").
		Position(ui.Dpi(450, 366)).
		Layout(ui.LAY_HOLD_MOVE))

	cmbTargetSizeImg := ui.NewComboBox(wnd, ui.OptsComboBox().
		Position(ui.Dpi(470, 360)).Width(100).
		Layout(ui.LAY_HOLD_MOVE).
		Texts(imageSizePresets.Labels()...).
		Select(1))

	// Right column — video row
	lblVideo := ui.NewStatic(wnd, ui.OptsStatic().
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

	lblVideoTo := ui.NewStatic(wnd, ui.OptsStatic().
		Text("to").
		Position(ui.Dpi(450, 401)).
		Layout(ui.LAY_HOLD_MOVE))

	cmbTargetSizeVid := ui.NewComboBox(wnd, ui.OptsComboBox().
		Position(ui.Dpi(470, 395)).Width(100).
		Layout(ui.LAY_HOLD_MOVE).
		Texts(videoSizePresets.Labels()...).
		Select(2))

	lblVideoQuality := ui.NewStatic(wnd, ui.OptsStatic().
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

	chkRegistry := ui.NewCheckBox(wnd, ui.OptsCheckBox().
		Text("Right-click menu").
		Position(ui.Dpi(170, 475)).
		Layout(ui.LAY_HOLD_MOVE))

	me := &MainWindow{
		wnd,
		lstView,
		lstPath,
		lstSortCol,
		lstSortAsc,
		btnAddFiles,
		btnRemoveSel,
		btnRemoveAll,
		verticalSep,
		lblImage,
		btnStripMetaImg,
		btnCompressImg,
		lblImageTo,
		cmbTargetSizeImg,
		lblVideo,
		btnStripMetaVid,
		btnCompressVid,
		lblVideoTo,
		cmbTargetSizeVid,
		lblVideoQuality,
		cmbQuality,
		chkOverwrite,
		chkRegistry,
	}

	return me
}

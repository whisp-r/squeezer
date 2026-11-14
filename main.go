package main

import (
	_ "embed"

	_ "github.com/whisp-r/squeezer/rsrc"

	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/rodrigocfd/windigo/co"
	"github.com/rodrigocfd/windigo/ui"

	"github.com/gen2brain/beeep"
)

// var passCounter atomic.Int64

var (
	uninstall = flag.Bool("uninstall", false, "Uninstall Squeezer")
	cleanup   = flag.Bool("cleanup", false, "Cleans up the process that called it (internal)")
)

func isDev() bool {
	self, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.Contains(self, "go-build")
}

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
	mainWindow := NewMainWindow()

	if ipc.TryBecomePrimary() == false {
		if len(args) > 0 {
			ipc.Send(args)
		} else {
			ui.NewMain(ui.OptsMain().Title("Squeezer").Size(ui.Dpi(1, 1))).Hwnd().
				MessageBox("Squeezer is already running.", "Squeezer", co.MB_ICONINFORMATION)
		}
	} else {
		if !isDev() {
			go installIfNeeded()
		}

		mainWindow.Events()
		mainWindow.OnIncomingIPC(ipc)
		mainWindow.main.RunAsMain()
	}
}

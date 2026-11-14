package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	_ "embed"
	"io"
	"os"
	"path/filepath"
	"sync"
)

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

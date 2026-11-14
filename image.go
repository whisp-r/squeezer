package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// IMAGE MANIPULATION

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

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
)

// VIDEO MANIPULATION

var passCounter atomic.Int64

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

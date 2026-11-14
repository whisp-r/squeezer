package main

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// WINDOWS REGISTRY

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

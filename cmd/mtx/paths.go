package main

import (
	"os"
	"path/filepath"
)

func xdgConfig() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "mtx")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "mtx")
}

func xdgData() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "mtx")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "mtx")
}

func xdgCache() string {
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return filepath.Join(dir, "mtx")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "mtx")
}

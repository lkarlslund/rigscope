//go:build linux

package collectors

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func init() {
	Register(Registration{
		Name: "zenpower",
		Detect: func() (Collector, bool, error) {
			path, err := FindZenpower()
			if err == nil {
				return Zenpower{Path: path}, true, nil
			}
			if errors.Is(err, os.ErrNotExist) {
				return nil, false, nil
			}
			return nil, false, err
		},
	})
}

type Zenpower struct {
	Path string
}

func (Zenpower) Name() string { return "zenpower" }

func (z Zenpower) Sample(context.Context) (map[string]any, error) {
	data, err := os.ReadFile(z.Path)
	if err != nil {
		return nil, err
	}
	microwatts, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"cpu_package_power_w": float64(microwatts) / 1_000_000,
		"path":                z.Path,
	}, nil
}

func FindZenpower() (string, error) {
	matches, err := filepath.Glob("/sys/class/hwmon/hwmon*")
	if err != nil {
		return "", err
	}
	for _, dir := range matches {
		name, err := os.ReadFile(filepath.Join(dir, "name"))
		if err != nil {
			continue
		}
		path := filepath.Join(dir, "power1_input")
		if strings.TrimSpace(string(name)) == "zenpower" {
			if _, err := os.Stat(path); err != nil {
				return "", err
			}
			return path, nil
		}
	}
	return "", os.ErrNotExist
}

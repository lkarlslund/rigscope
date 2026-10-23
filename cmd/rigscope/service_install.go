//go:build windows || darwin || linux

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// installBinary copies the running executable to dst, unless it already is dst.
// The copy goes through a temporary file and a rename so a half-written binary
// never sits at dst.
func installBinary(out io.Writer, dst string) error {
	src, err := os.Executable()
	if err != nil {
		return err
	}
	if same, _ := sameFile(src, dst); same {
		_, err = fmt.Fprintln(out, "binary:", dst, "(already installed)")
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		_ = in.Close()
	}()
	tmp := dst + ".new"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, in); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_, err = fmt.Fprintln(out, "binary:", dst)
	return err
}

func sameFile(a, b string) (bool, error) {
	ai, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(ai, bi), nil
}

// removeServiceData deletes the data directory and log file after uninstall.
func removeServiceData(out io.Writer, cfg serviceConfig) error {
	if err := os.RemoveAll(cfg.dataDir); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "removed data:", cfg.dataDir); err != nil {
		return err
	}
	if cfg.logFile == "" {
		return nil
	}
	for _, path := range []string{cfg.logFile, cfg.logFile + ".1"} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	// Drop the log directory too when nothing else lives there.
	_ = os.Remove(filepath.Dir(cfg.logFile))
	return nil
}

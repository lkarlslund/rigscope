//go:build !windows && !darwin && !linux

package main

import (
	"errors"
	"io"
)

const (
	serviceSupported   = false
	serviceManagerName = "unsupported"
	serviceLogsViaFlag = false
)

var errServiceUnsupported = errors.New("service management is not supported on this platform")

func defaultServiceConfig() serviceConfig { return serviceConfig{} }

func serviceInstall(io.Writer, serviceConfig, bool) error   { return errServiceUnsupported }
func serviceUninstall(io.Writer, serviceConfig, bool) error { return errServiceUnsupported }
func serviceStart(io.Writer, serviceConfig) error           { return errServiceUnsupported }
func serviceStop(io.Writer) error                           { return errServiceUnsupported }
func serviceRestart(io.Writer, serviceConfig) error         { return errServiceUnsupported }
func serviceStatus(io.Writer) error                         { return errServiceUnsupported }

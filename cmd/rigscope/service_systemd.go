package main

import (
	"path/filepath"
	"strings"
)

const (
	systemdUser  = "rigscope"
	systemdGroup = "rigscope"
)

// systemdUnit renders the rigscope.service unit for cfg. It lives outside the
// linux build so tests cover it on every platform.
func systemdUnit(cfg serviceConfig) string {
	execStart := make([]string, 0, 16)
	for _, arg := range append([]string{cfg.binary}, cfg.serveArgs()...) {
		execStart = append(execStart, systemdQuote(arg))
	}
	installDir := filepath.Dir(cfg.binary)

	return `[Unit]
Description=rigscope telemetry daemon
Documentation=https://github.com/lkarlslund/rigscope
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=` + systemdUser + `
Group=` + systemdGroup + `
WorkingDirectory=` + systemdQuote(installDir) + `
ExecStart=` + strings.Join(execStart, " ") + `
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
ReadWritePaths=` + systemdQuote(cfg.dataDir) + `

[Install]
WantedBy=multi-user.target
`
}

// systemdQuote escapes a value for a unit file: specifiers (%) and variable
// expansion ($) are doubled, and values with whitespace or quotes are wrapped
// in double quotes.
func systemdQuote(s string) string {
	s = strings.NewReplacer("%", "%%", "$", "$$").Replace(s)
	if s != "" && !strings.ContainsAny(s, " \t\n\"'\\;") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s) + `"`
}

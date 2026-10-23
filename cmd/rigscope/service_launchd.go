package main

import (
	"bytes"
	"encoding/xml"
	"path/filepath"
)

const launchdLabel = "com.github.lkarlslund.rigscope"

// launchdPlist renders the LaunchDaemon property list for cfg. It lives outside
// the darwin build so tests cover it on every platform.
func launchdPlist(cfg serviceConfig) []byte {
	var b bytes.Buffer
	str := func(s string) {
		b.WriteString("<string>")
		_ = xml.EscapeText(&b, []byte(s))
		b.WriteString("</string>")
	}
	key := func(k string) {
		b.WriteString("\t<key>")
		b.WriteString(k)
		b.WriteString("</key>")
	}

	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
`)
	key("Label")
	str(launchdLabel)
	b.WriteString("\n")
	key("ProgramArguments")
	b.WriteString("\n\t<array>\n")
	for _, arg := range append([]string{cfg.binary}, cfg.serveArgs()...) {
		b.WriteString("\t\t")
		str(arg)
		b.WriteString("\n")
	}
	b.WriteString("\t</array>\n")
	key("WorkingDirectory")
	str(filepath.Dir(cfg.dataDir))
	b.WriteString("\n")
	key("RunAtLoad")
	b.WriteString("<true/>\n")
	// Restart after crashes and error exits, but stay down after a clean stop.
	key("KeepAlive")
	b.WriteString("<dict><key>SuccessfulExit</key><false/></dict>\n")
	key("ThrottleInterval")
	b.WriteString("<integer>3</integer>\n")
	if cfg.logFile != "" {
		key("StandardOutPath")
		str(cfg.logFile)
		b.WriteString("\n")
		key("StandardErrorPath")
		str(cfg.logFile)
		b.WriteString("\n")
	}
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes()
}

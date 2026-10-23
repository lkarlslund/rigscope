package main

import (
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func testServiceConfig() serviceConfig {
	return serviceConfig{
		binary:    "/opt/rig scope/rigscope",
		dataDir:   "/var/lib/rigscope & co/data",
		logFile:   "/var/log/rigscope/rigscope.log",
		addr:      "127.0.0.1:7077",
		interval:  500 * time.Millisecond,
		retention: 0,
		logLevel:  "debug",
	}
}

func TestServiceServeArgs(t *testing.T) {
	args := testServiceConfig().serveArgs()
	want := []string{
		"serve",
		"--addr", "127.0.0.1:7077",
		"--data-dir", "/var/lib/rigscope & co/data",
		"--interval", "500ms",
		"--retention", "0s",
		"--log-level", "debug",
	}
	if serviceLogsViaFlag {
		want = append(want, "--log-file", "/var/log/rigscope/rigscope.log")
	}
	if !slices.Equal(args, want) {
		t.Fatalf("serveArgs() = %q, want %q", args, want)
	}

	// The args must parse as a serve invocation.
	cmd := newRootCommand(os.Stdout, os.Stderr)
	serve, rest, err := cmd.Find(args)
	if err != nil || serve.Name() != "serve" {
		t.Fatalf("Find(%q) = %v, %v", args, serve, err)
	}
	if err := serve.ParseFlags(rest); err != nil {
		t.Fatalf("serve.ParseFlags(%q) error = %v", rest, err)
	}
}

func TestLaunchdPlistIsValidXML(t *testing.T) {
	cfg := testServiceConfig()
	plist := launchdPlist(cfg)

	decoder := xml.NewDecoder(strings.NewReader(string(plist)))
	decoder.Strict = true
	var strs []string
	inString := false
	for {
		tok, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("invalid plist XML: %v\n%s", err, plist)
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			inString = tok.Name.Local == "string"
		case xml.CharData:
			if inString {
				strs = append(strs, string(tok))
			}
		case xml.EndElement:
			inString = false
		}
	}

	wantArgs := append([]string{cfg.binary}, cfg.serveArgs()...)
	if !slices.Contains(strs, launchdLabel) {
		t.Fatalf("plist strings %q missing label", strs)
	}
	for _, arg := range wantArgs {
		if !slices.Contains(strs, arg) {
			t.Fatalf("plist strings %q missing program argument %q", strs, arg)
		}
	}
	if !slices.Contains(strs, cfg.logFile) {
		t.Fatalf("plist strings %q missing log path", strs)
	}
}

func TestOpenLogFileRotatesLargeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "rigscope.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, maxLogFileSize+1), 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := openLogFile(path)
	if err != nil {
		t.Fatalf("openLogFile() error = %v", err)
	}
	if _, err := f.WriteString("fresh\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	if got, _ := os.ReadFile(path); string(got) != "fresh\n" {
		t.Fatalf("log after rotation = %q, want fresh content", got)
	}
	if info, err := os.Stat(path + ".1"); err != nil || info.Size() != maxLogFileSize+1 {
		t.Fatalf("rotated log stat = %v, %v", info, err)
	}
}

func TestServiceCommandPresence(t *testing.T) {
	cmd := newRootCommand(os.Stdout, os.Stderr)
	found, _, err := cmd.Find([]string{"service", "install"})
	got := err == nil && found.Name() == "install"
	if got != serviceSupported {
		t.Fatalf("service install command present = %v, want %v", got, serviceSupported)
	}
}

func TestSystemdUnit(t *testing.T) {
	cfg := serviceConfig{
		binary:    "/opt/rigscope/rigscope",
		dataDir:   "/opt/rigscope/data",
		addr:      "0.0.0.0:7077",
		interval:  time.Second,
		retention: 0,
		logLevel:  "info",
	}
	unit := systemdUnit(cfg)
	for _, want := range []string{
		"User=rigscope\n",
		"WorkingDirectory=/opt/rigscope\n",
		"ExecStart=/opt/rigscope/rigscope serve --addr 0.0.0.0:7077 --data-dir /opt/rigscope/data --interval 1s --retention 0s --log-level info\n",
		"Restart=on-failure\n",
		"ReadWritePaths=/opt/rigscope/data\n",
		"WantedBy=multi-user.target\n",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit missing %q:\n%s", want, unit)
		}
	}
}

func TestSystemdQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/opt/rigscope/data":  "/opt/rigscope/data",
		"/srv/rig scope/data": `"/srv/rig scope/data"`,
		"/srv/100%/$HOME":     "/srv/100%%/$$HOME",
		`/srv/say "hi"\there`: `"/srv/say \"hi\"\\there"`,
		"":                    `""`,
	} {
		if got := systemdQuote(in); got != want {
			t.Errorf("systemdQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

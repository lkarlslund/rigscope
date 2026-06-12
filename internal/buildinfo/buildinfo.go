package buildinfo

import (
	"os"
	"time"
)

var (
	Version = "dev"
	Commit  = "unknown"
	BuiltAt = "unknown"

	startedAt = time.Now().UTC()
)

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuiltAt   string `json:"built_at"`
	StartedAt string `json:"started_at"`
	PID       int    `json:"pid"`
}

func Current() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		BuiltAt:   BuiltAt,
		StartedAt: startedAt.Format(time.RFC3339Nano),
		PID:       os.Getpid(),
	}
}

package events

import (
	"encoding/json"
	"fmt"
	"strings"
)

const Prefix = "POWERBENCH "

type ParsedLine struct {
	Event map[string]any
	Raw   string
	Err   error
}

func ParseLine(line string) ParsedLine {
	raw := strings.TrimRight(line, "\n")
	if !strings.HasPrefix(raw, Prefix) {
		return ParsedLine{Raw: raw}
	}

	var event map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw[len(Prefix):])), &event); err != nil {
		return ParsedLine{Raw: raw, Err: fmt.Errorf("invalid POWERBENCH json: %w", err)}
	}
	if event == nil {
		return ParsedLine{Raw: raw, Err: fmt.Errorf("POWERBENCH payload must be a JSON object")}
	}
	return ParsedLine{Event: event, Raw: raw}
}

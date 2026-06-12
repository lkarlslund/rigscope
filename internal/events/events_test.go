package events

import "testing"

func TestParseLine(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantEvent bool
		wantErr   bool
	}{
		{
			name:      "plain output",
			line:      "hello",
			wantEvent: false,
		},
		{
			name:      "event",
			line:      `POWERBENCH {"type":"point","name":"prompt_tps","value":123.4}`,
			wantEvent: true,
		},
		{
			name:    "bad json",
			line:    `POWERBENCH {"type":`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseLine(tt.line)
			if (got.Event != nil) != tt.wantEvent {
				t.Fatalf("event presence = %v, want %v", got.Event != nil, tt.wantEvent)
			}
			if (got.Err != nil) != tt.wantErr {
				t.Fatalf("err presence = %v, want %v", got.Err != nil, tt.wantErr)
			}
		})
	}
}

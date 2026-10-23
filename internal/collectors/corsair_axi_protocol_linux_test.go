//go:build linux

package collectors

import (
	"bytes"
	"errors"
	"testing"
)

func TestAXiEncodeDecodeRoundTrip(t *testing.T) {
	payload := []byte{0x13, 0x03, 0x06, 0x01, 0x07, 0x02, 0x97, 0x00, 0xff}
	encoded := axiEncode(payload)
	if len(encoded) != len(payload)*2+2 || encoded[0] != 0x54 || encoded[len(encoded)-1] != 0 {
		t.Fatalf("encoded = % x", encoded)
	}
	// Replies carry a different header than requests.
	reply := append([]byte{0xa8}, encoded[1:]...)
	got, err := axiDecode(reply)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("decoded % x, want % x", got, payload)
	}
}

func TestAXiDecodeCapturedModelReply(t *testing.T) {
	// Reply to a 7-byte read of register 0x9a from an AX1600i.
	reply := []byte{0xa8, 0x56, 0x65, 0x95, 0x66, 0x56, 0x5a, 0x69, 0x5a, 0x55, 0x5a, 0x55, 0x5a, 0x96, 0x69, 0x00}
	got, err := axiDecode(reply)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "AX1600i" {
		t.Fatalf("decoded %q", got)
	}
}

func TestAXiDecodeRejectsCorruptReplies(t *testing.T) {
	for _, reply := range [][]byte{
		{0x00},
		{0x54, 0x55, 0x55, 0x00}, // request header
		{0xa8, 0x55, 0x00},       // odd length
		{0xa8, 0x55, 0x12, 0x00}, // not a data symbol
		{0xa8, 0x55, 0x55},       // unterminated
	} {
		if _, err := axiDecode(reply); err == nil {
			t.Errorf("axiDecode(% x) succeeded", reply)
		}
	}
}

func TestLinear11(t *testing.T) {
	if got := linear11([]byte{0xc5, 0x00}); got != 197 {
		t.Fatalf("linear11(c5 00) = %v, want 197", got)
	}
	// Exponent -2 (0b11110), mantissa 7: 1.75.
	if got := linear11([]byte{0x07, 0xf0}); got != 1.75 {
		t.Fatalf("linear11(07 f0) = %v, want 1.75", got)
	}
	// Exponent 0, mantissa -1024.
	if got := linear11([]byte{0x00, 0x04}); got != -1024 {
		t.Fatalf("linear11(00 04) = %v, want -1024", got)
	}
}

// scriptedAXi replays dongle replies and records the payloads it was sent.
type scriptedAXi struct {
	replies [][]byte
	sent    [][]byte
}

func (s *scriptedAXi) exchange(payload []byte) ([]byte, error) {
	s.sent = append(s.sent, append([]byte(nil), payload...))
	if len(s.replies) == 0 {
		return nil, errors.New("no scripted reply")
	}
	reply := s.replies[0]
	s.replies = s.replies[1:]
	return reply, nil
}

func TestAXiReadPollsWhileBusy(t *testing.T) {
	s := &scriptedAXi{replies: [][]byte{
		{},                 // command accepted
		{axiStatusBusy},    // still busy
		{axiStatusOK},      // done
		{0xc5, 0x00, 0x99}, // memory, longer than requested
	}}
	v, err := axiPSU{t: s}.readLinear(axiRegInputPower)
	if err != nil {
		t.Fatal(err)
	}
	if v != 197 {
		t.Fatalf("value = %v, want 197", v)
	}
	want := [][]byte{
		{axiActionSMBusCommand, 0x03, 0x06, 0x01, 0x07, 0x02, axiRegInputPower},
		{axiActionReadSMBusStatus},
		{axiActionReadSMBusStatus},
		{axiActionReadMemory, 0x07, 0x02},
	}
	if len(s.sent) != len(want) {
		t.Fatalf("sent %d messages, want %d: % x", len(s.sent), len(want), s.sent)
	}
	for i := range want {
		if !bytes.Equal(s.sent[i], want[i]) {
			t.Errorf("message %d = % x, want % x", i, s.sent[i], want[i])
		}
	}
}

func TestAXiWriteCountsRegisterInLength(t *testing.T) {
	s := &scriptedAXi{replies: [][]byte{{axiStatusOK}, {axiStatusOK}}}
	if err := (axiPSU{t: s}).write(axiReg12VPage, 3); err != nil {
		t.Fatal(err)
	}
	if want := []byte{axiActionSMBusCommand, 0x01, 0x04, 0x02, axiReg12VPage, 3}; !bytes.Equal(s.sent[0], want) {
		t.Fatalf("write = % x, want % x", s.sent[0], want)
	}
}

func TestAXiReportsDongleErrors(t *testing.T) {
	s := &scriptedAXi{replies: [][]byte{{0xff}}}
	if _, err := (axiPSU{t: s}).read(axiRegModel, 7); err == nil {
		t.Fatal("read succeeded despite an error status")
	}
}

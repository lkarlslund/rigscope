//go:build linux

package collectors

import (
	"errors"
	"fmt"
	"math"
)

// Corsair AXi power supplies (AX760i through AX1600i) expose a PMBus-style
// register file through a SiLabs USB-to-SMBus bridge. The protocol was
// documented by the cpsumon, ax1600i and FanControl.CorsairLink projects; this
// is an independent implementation.
//
// Every message is encoded as a header byte, two symbols per payload byte (low
// nibble first) and a terminating zero.

var axiEncodeTable = [16]byte{0x55, 0x56, 0x59, 0x5a, 0x65, 0x66, 0x69, 0x6a, 0x95, 0x96, 0x99, 0x9a, 0xa5, 0xa6, 0xa9, 0xaa}

// axiDecodeTable maps a symbol to 0x20|nibble for data symbols and to
// 0x10|nibble for header symbols (data symbols with the low two bits cleared).
var axiDecodeTable = func() [256]byte {
	var t [256]byte
	for i := 15; i >= 0; i-- {
		t[axiEncodeTable[i]&0xfc] = 0x10 | byte(i&^1)
	}
	for i, symbol := range axiEncodeTable {
		t[symbol] = 0x20 | byte(i)
	}
	return t
}()

// Dongle actions (first payload byte).
const (
	axiActionReadMemory      = 0x08
	axiActionSMBusSettings   = 0x11
	axiActionReadSMBusStatus = 0x12
	axiActionSMBusCommand    = 0x13
)

// Dongle status replies.
const (
	axiStatusOK   = 0x00
	axiStatusBusy = 0x80
)

// PSU registers.
const (
	axiRegPage          = 0x00
	axiRegInputVoltage  = 0x88
	axiRegInputCurrent  = 0x89
	axiRegOutputVoltage = 0x8b
	axiRegOutputCurrent = 0x8c
	axiRegTemp1         = 0x8d
	axiRegTemp2         = 0x8e
	axiRegFanSpeed      = 0x90
	axiRegOutputPower   = 0x96
	axiRegInputPower    = 0x97
	axiRegModel         = 0x9a
	axiReg12VPage       = 0xe7
	axiReg12VPower      = 0xe9
	axiRegTotalOutPower = 0xee
)

func axiEncode(payload []byte) []byte {
	out := make([]byte, 0, len(payload)*2+2)
	out = append(out, axiEncodeTable[0]&0xfc) // command 0
	for _, b := range payload {
		out = append(out, axiEncodeTable[b&0x0f], axiEncodeTable[b>>4])
	}
	return append(out, 0)
}

func axiDecode(data []byte) ([]byte, error) {
	if len(data) < 2 || data[len(data)-1] != 0 {
		return nil, fmt.Errorf("truncated reply % x", data)
	}
	if (axiDecodeTable[data[0]]&0x0f)>>1 != 7 {
		return nil, fmt.Errorf("bad reply header % x", data)
	}
	body := data[1 : len(data)-1]
	if len(body)%2 != 0 {
		return nil, fmt.Errorf("odd reply length % x", data)
	}
	out := make([]byte, 0, len(body)/2)
	for i := 0; i < len(body); i += 2 {
		lo, hi := axiDecodeTable[body[i]], axiDecodeTable[body[i+1]]
		if lo&0xf0 != 0x20 || hi&0xf0 != 0x20 {
			return nil, fmt.Errorf("bad reply symbol in % x", data)
		}
		out = append(out, lo&0x0f|(hi&0x0f)<<4)
	}
	return out, nil
}

// linear11 decodes a PMBus LINEAR11 value: a 5-bit signed exponent over an
// 11-bit signed mantissa.
func linear11(b []byte) float64 {
	v := uint16(b[0]) | uint16(b[1])<<8
	exponent := int(int16(v) >> 11)
	mantissa := int(int16(v<<5) >> 5)
	return math.Ldexp(float64(mantissa), exponent)
}

// axiTransport exchanges one encoded message with the dongle.
type axiTransport interface {
	exchange(payload []byte) ([]byte, error)
}

var errAXiBusy = errors.New("PSU stayed busy")

type axiPSU struct {
	t axiTransport
}

func (p axiPSU) setup() error {
	return p.expectOK([]byte{axiActionSMBusSettings, 0x02, 0x64, 0, 0, 0, 0})
}

func (p axiPSU) expectOK(payload []byte) error {
	reply, err := p.t.exchange(payload)
	if err != nil {
		return err
	}
	if len(reply) > 0 && reply[0] != axiStatusOK {
		return fmt.Errorf("dongle status %#02x for action %#02x", reply[0], payload[0])
	}
	return nil
}

func (p axiPSU) waitIdle() error {
	for range 50 {
		reply, err := p.t.exchange([]byte{axiActionReadSMBusStatus})
		if err != nil {
			return err
		}
		if len(reply) == 0 || reply[0] == axiStatusOK {
			return nil
		}
		if reply[0] != axiStatusBusy {
			return fmt.Errorf("dongle status %#02x", reply[0])
		}
	}
	return errAXiBusy
}

func (p axiPSU) read(reg byte, n int) ([]byte, error) {
	if err := p.expectOK([]byte{axiActionSMBusCommand, 0x03, 0x06, 0x01, 0x07, byte(n), reg}); err != nil {
		return nil, fmt.Errorf("register %#02x: %w", reg, err)
	}
	if err := p.waitIdle(); err != nil {
		return nil, fmt.Errorf("register %#02x: %w", reg, err)
	}
	data, err := p.t.exchange([]byte{axiActionReadMemory, 0x07, byte(n)})
	if err != nil {
		return nil, fmt.Errorf("register %#02x: %w", reg, err)
	}
	if len(data) < n {
		return nil, fmt.Errorf("register %#02x: short reply % x", reg, data)
	}
	return data[:n], nil
}

func (p axiPSU) write(reg byte, data ...byte) error {
	payload := append([]byte{axiActionSMBusCommand, 0x01, 0x04, byte(len(data) + 1), reg}, data...)
	if err := p.expectOK(payload); err != nil {
		return fmt.Errorf("register %#02x: %w", reg, err)
	}
	if err := p.waitIdle(); err != nil {
		return fmt.Errorf("register %#02x: %w", reg, err)
	}
	return nil
}

func (p axiPSU) readLinear(reg byte) (float64, error) {
	b, err := p.read(reg, 2)
	if err != nil {
		return 0, err
	}
	return linear11(b), nil
}

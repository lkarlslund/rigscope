//go:build linux

package collectors

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	corsairVendorID = 0x1b1c
	axiOutEndpoint  = 0x02
	axiInEndpoint   = 0x82
	axiUSBTimeout   = 500 * time.Millisecond
	axiMessageGap   = time.Millisecond

	// 12V connector pages; unused pages read 0 W.
	axi12VPages = 12
)

// axiProductIDs lists AXi dongles that use the polled ("modern") protocol.
var axiProductIDs = []uint16{0x1c11}

func init() {
	Register(Registration{
		Name: "corsair_axi",
		Detect: func() (Collector, bool, error) {
			for _, product := range axiProductIDs {
				path, err := findUSBDevice("/sys/bus/usb/devices", corsairVendorID, product)
				if err == nil {
					return &CorsairAXi{DevicePath: path}, true, nil
				}
			}
			return nil, false, nil
		},
	})
}

// CorsairAXi reads a Corsair AXi PSU through its USB dongle. Opening the
// device node needs write access, typically granted by a udev rule.
type CorsairAXi struct {
	DevicePath string

	mu    sync.Mutex
	dev   *usbDevice
	psu   axiPSU
	model string

	connector         int
	connectorSelected bool
	connectorRead     [axi12VPages]bool
	connectorPower    [axi12VPages]float64
}

func (*CorsairAXi) Name() string { return "corsair_axi" }

type axiUSBTransport struct {
	dev *usbDevice
}

func (t axiUSBTransport) exchange(payload []byte) ([]byte, error) {
	msg := axiEncode(payload)
	// The bridge drops messages that follow the previous reply too closely.
	time.Sleep(axiMessageGap)
	if _, err := t.dev.bulk(axiOutEndpoint, msg, axiUSBTimeout); err != nil {
		return nil, fmt.Errorf("usb write: %w", err)
	}
	var reply []byte
	buf := make([]byte, 64)
	for len(reply) == 0 || reply[len(reply)-1] != 0 {
		n, err := t.dev.bulk(axiInEndpoint, buf, axiUSBTimeout)
		if err != nil {
			return nil, fmt.Errorf("usb read: %w", err)
		}
		reply = append(reply, buf[:n]...)
		if len(reply) > 4096 {
			return nil, errors.New("unterminated reply")
		}
	}
	return axiDecode(reply)
}

func (c *CorsairAXi) open() error {
	dev, err := openUSBDevice(c.DevicePath, 0)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("%w: no write access to %s; install a udev rule for USB %04x:%04x", err, c.DevicePath, corsairVendorID, axiProductIDs[0])
		}
		return err
	}
	// Enable the SiLabs USBXpress bridge (clear to send).
	if err := dev.control(0x40, 0x02, 0x0002, 0, axiUSBTimeout); err != nil {
		_ = dev.Close()
		return fmt.Errorf("initialising dongle: %w", err)
	}
	psu := axiPSU{t: axiUSBTransport{dev: dev}}
	if err := psu.setup(); err != nil {
		_ = dev.Close()
		return fmt.Errorf("configuring dongle: %w", err)
	}
	model, err := psu.model()
	if err != nil {
		_ = dev.Close()
		return fmt.Errorf("reading model: %w", err)
	}
	c.dev, c.psu, c.model = dev, psu, model
	return nil
}

func (c *CorsairAXi) closeDevice() {
	if c.dev != nil {
		_ = c.dev.Close()
		c.dev = nil
	}
	c.connectorSelected = false
}

func (c *CorsairAXi) Sample(context.Context) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dev == nil {
		if err := c.open(); err != nil {
			return nil, err
		}
	}
	m, err := c.read()
	if err != nil {
		// The dongle is known to wedge after hours of polling; reopen next time.
		c.closeDevice()
		return nil, err
	}
	return metricRecord(m), nil
}

func (c *CorsairAXi) read() ([]map[string]any, error) {
	p := c.psu
	base := map[string]string{"device": c.model}
	labels := func(kv ...string) map[string]string {
		out := cloneLabels(base)
		for i := 0; i+1 < len(kv); i += 2 {
			out[kv[i]] = kv[i+1]
		}
		return out
	}
	var m []map[string]any
	readings := []struct {
		reg                        byte
		name, unit, sym, kind, lbl string
	}{
		{axiRegInputPower, "psu_input_power_w", "watt", "W", "power_input", "PSU input"},
		{axiRegTotalOutPower, "psu_output_power_w", "watt", "W", "power_component", "PSU output"},
		{axiRegInputVoltage, "psu_input_voltage_v", "volt", "V", "voltage", "PSU input"},
		{axiRegInputCurrent, "psu_input_current_a", "ampere", "A", "current", "PSU input"},
		{axiRegTemp1, "temperature_celsius", "celsius", "°C", "temperature", "PSU 1"},
		{axiRegTemp2, "temperature_celsius", "celsius", "°C", "temperature", "PSU 2"},
		{axiRegFanSpeed, "psu_fan_rpm", "rpm", "RPM", "fan", "PSU fan"},
	}
	values := map[byte]float64{}
	for _, r := range readings {
		v, err := p.readLinear(r.reg)
		if err != nil {
			return nil, err
		}
		values[r.reg] = v
		m = append(m, metric(r.name, v, r.unit, r.sym, r.kind, labels("sensor", r.lbl)))
	}
	if in, out := values[axiRegInputPower], values[axiRegTotalOutPower]; in > 0 && out > 0 && out <= in {
		m = append(m, metric("psu_efficiency_pct", out/in*100, "percent", "%", "efficiency", labels("sensor", "PSU efficiency")))
	}

	for page, rail := range []string{"12V", "5V", "3.3V"} {
		if err := p.write(axiRegPage, byte(page)); err != nil {
			return nil, err
		}
		for _, r := range []struct {
			reg                   byte
			name, unit, sym, kind string
		}{
			{axiRegOutputVoltage, "psu_rail_voltage_v", "volt", "V", "voltage"},
			{axiRegOutputCurrent, "psu_rail_current_a", "ampere", "A", "current"},
			{axiRegOutputPower, "psu_rail_power_w", "watt", "W", "power_component"},
		} {
			v, err := p.readLinear(r.reg)
			if err != nil {
				return nil, err
			}
			m = append(m, metric(r.name, v, r.unit, r.sym, r.kind, labels("rail", rail, "sensor", "PSU "+rail)))
		}
	}

	// The 12V connector monitor takes about 50 ms to settle after its page
	// changes, too long to sweep every connector in one sample. Instead read
	// the page selected on the previous sample, select the next one, and
	// report the latest reading of each connector.
	if err := p.write(axiRegPage, 0); err != nil {
		return nil, err
	}
	if c.connectorSelected {
		power, err := p.readLinear(axiReg12VPower)
		if err != nil {
			return nil, err
		}
		c.connectorPower[c.connector] = power
		c.connectorRead[c.connector] = true
		c.connector = (c.connector + 1) % axi12VPages
	}
	if err := p.write(axiReg12VPage, byte(c.connector)); err != nil {
		return nil, err
	}
	c.connectorSelected = true
	for page := range axi12VPages {
		if !c.connectorRead[page] {
			continue
		}
		connector := strconv.Itoa(page + 1)
		m = append(m, metric("psu_connector_power_w", c.connectorPower[page], "watt", "W", "power_component",
			labels("rail", "12V", "connector", connector, "sensor", "PSU 12V #"+connector)))
	}
	return m, nil
}

func (p axiPSU) model() (string, error) {
	b, err := p.read(axiRegModel, 7)
	if err != nil {
		return "", err
	}
	end := len(b)
	for end > 0 && (b[end-1] == 0 || b[end-1] == ' ') {
		end--
	}
	return string(b[:end]), nil
}

//go:build linux

package collectors

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestCorsairAXiProbe reads a real PSU. Run it with RIGSCOPE_AXI_PROBE=1.
func TestCorsairAXiProbe(t *testing.T) {
	if os.Getenv("RIGSCOPE_AXI_PROBE") == "" {
		t.Skip("set RIGSCOPE_AXI_PROBE=1 to read a connected Corsair AXi PSU")
	}
	path, err := findUSBDevice("/sys/bus/usb/devices", corsairVendorID, axiProductIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	c := &CorsairAXi{DevicePath: path}
	defer c.closeDevice()
	for i := range 14 {
		start := time.Now()
		sample, err := c.Sample(context.Background())
		if err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
		t.Logf("sample %d took %s, model %q", i, time.Since(start), c.model)
		if i == 13 {
			for _, m := range sample["metrics"].([]map[string]any) {
				t.Logf("%-24s %9.3f %-4s %v", m["name"], m["value"], m["symbol"], m["labels"])
			}
		}
	}
}

package aircraft

import (
	"os"
	"testing"
)

func TestScanRealFolder(t *testing.T) {
	dir := os.Getenv("TANDEM_SCAN_DIR")
	if dir == "" {
		t.Skip("TANDEM_SCAN_DIR not set - the real community folder is not part of the test suite")
	}
	for _, p := range Scan(dir) {
		t.Logf("%-9s %-52s %-6s objs=%d v=%s", p.Kind, p.Label(), p.TypeCode, p.SimObjects, p.Version)
	}
}

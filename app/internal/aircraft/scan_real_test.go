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
	pkgs := Aircraft(Scan(dir))
	for _, p := range Scan(dir) {
		t.Logf("%-9s %-52s %-6s objs=%d v=%s", p.Kind, p.Label(), p.TypeCode, p.SimObjects, p.Version)
	}
	t.Logf("flight files: %d", len(FlightFiles()))
	for i, f := range FlightFiles() {
		if i > 3 {
			break
		}
		t.Logf("   %s", f)
	}
	p, why, ok := FromFlightFiles(pkgs)
	t.Logf("from the flight files: ok=%v %q (%s) out of %d aircraft", ok, p.Label(), why, len(pkgs))
}

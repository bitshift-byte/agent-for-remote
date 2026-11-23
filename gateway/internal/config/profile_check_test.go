package config

import (
 "os"
 "testing"
)

func TestSmoothProfileDefaults(t *testing.T) {
 os.Unsetenv("DESKTOP_PERFORMANCE_PROFILE")
 os.Unsetenv("SELKIES_USE_PAINT_OVER_QUALITY")
 c, err := Load()
 if err != nil { t.Fatal(err) }
 if c.PerformanceProfile != "smooth" || c.SelkiesPaintOverQuality != "false" || !c.SelkiesVideoStreamingMode || c.SelkiesRateControlMode != "cbr" || c.SelkiesVideoBitrate != "2500" || c.SelkiesFramerate != "30" || c.SelkiesManualWidth != 1280 || c.SelkiesManualHeight != 720 || c.SelkiesEnableResize || c.SelkiesBackpressureQueueSize != 30 || !c.SelkiesCongestionControl || c.DesktopCPUs != 4 {
   t.Fatalf("smooth defaults wrong: %+v", c)
 }
 os.Setenv("DESKTOP_PERFORMANCE_PROFILE", "quality")
 defer os.Unsetenv("DESKTOP_PERFORMANCE_PROFILE")
 c2, _ := Load()
 if c2.SelkiesPaintOverQuality != "true" || c2.SelkiesVideoStreamingMode || c2.SelkiesManualWidth != 0 || !c2.SelkiesEnableResize {
   t.Fatalf("quality defaults wrong: %+v", c2)
 }
}

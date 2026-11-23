package syncer

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"time"
)

type FileConfig struct {
	Server     string `json:"server"`
	Username   string `json:"username"`
	Token      string `json:"token"`
	Dir        string `json:"dir"`
	DeviceName string `json:"device_name"`
	IntervalMs int    `json:"interval_ms"`
	Force      bool   `json:"force"`
}

func Main() {
	cfgFile := flag.String("config", "sync.json", "Path to sync configuration JSON file")
	flag.Parse()

	raw, err := os.ReadFile(*cfgFile)
	if err != nil {
		log.Fatalf("unable to read %s: %v", *cfgFile, err)
	}
	var fc FileConfig
	if err := json.Unmarshal(raw, &fc); err != nil {
		log.Fatalf("unable to parse %s: %v", *cfgFile, err)
	}

	interval := 800 * time.Millisecond
	if fc.IntervalMs > 0 {
		interval = time.Duration(fc.IntervalMs) * time.Millisecond
	}
	if fc.Dir == "" {
		fc.Dir = "."
	}

	err = RunDaemon(context.Background(), RunOptions{
		ServerURL:  fc.Server,
		Username:   fc.Username,
		Secret:     fc.Token,
		LocalDir:   fc.Dir,
		DeviceName: fc.DeviceName,
		Interval:   interval,
		Force:      fc.Force,
	})
	if err != nil {
		log.Fatal(err)
	}
}

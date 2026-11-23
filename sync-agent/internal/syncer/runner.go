package syncer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

type RunOptions struct {
	ServerURL  string
	Username   string
	Secret     string
	LocalDir   string
	DeviceName string
	Interval   time.Duration
	Force      bool
}

func RunDaemon(ctx context.Context, opts RunOptions) error {
	if opts.Interval <= 0 {
		opts.Interval = 800 * time.Millisecond
	}

	identity, err := LoadOrCreateDeviceIdentity(opts.LocalDir, opts.DeviceName)
	if err != nil {
		return fmt.Errorf("load device identity: %w", err)
	}

	client := NewClient(opts.ServerURL)
	if _, err := client.Login(ctx, opts.Username, opts.Secret); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}
	log.Printf("[auth] logged in as %s on %s", opts.Username, opts.ServerURL)

	acq, err := acquireLockInteractive(ctx, client, identity, opts.Force)
	if err != nil {
		return fmt.Errorf("acquire active device lock: %w", err)
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = client.ReleaseLock(releaseCtx)
		log.Println("[lock] released active device lock")
	}()

	engine, err := NewEngine(opts.LocalDir, client)
	if err != nil {
		return fmt.Errorf("init sync engine: %w", err)
	}

	if acq.TakenOver {
		log.Printf("[lock] took over workspace from %q — pulling latest cloud files first...", acq.PreviousDev)
		stats, err := engine.InitialTakeoverPull(ctx)
		if err != nil {
			return fmt.Errorf("initial takeover pull: %w", err)
		}
		log.Printf("[sync] takeover pull complete (pulled=%d)", stats.Pulled)
	} else {
		log.Printf("[lock] active device lock acquired (device=%s, epoch=%d)", identity.DeviceName, acq.Lock.Epoch)
	}

	log.Printf("[sync] watching %s <-> %s/workspace (interval=%s, ignoring node_modules/.venv/target/.git)", opts.LocalDir, opts.ServerURL, opts.Interval)

	if err := runSyncPass(ctx, engine); err != nil {
		if errors.Is(err, ErrLockLost) {
			log.Println("[lock] another device took over the workspace lock — sync paused automatically.")
			return nil
		}
		log.Printf("[sync] initial pass error: %v", err)
	} else {
		log.Println("[sync] initial sync check complete — watching for changes...")
	}

	syncTicker := time.NewTicker(opts.Interval)
	hbTicker := time.NewTicker(15 * time.Second)
	defer syncTicker.Stop()
	defer hbTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[sync] shutting down...")
			return nil
		case <-hbTicker.C:
			if err := client.Heartbeat(ctx); err != nil {
				if errors.Is(err, ErrLockLost) {
					log.Println("[lock] another device took over the workspace lock — sync paused automatically.")
					return nil
				}
				log.Printf("[lock] heartbeat warning: %v", err)
			} else {
				log.Printf("[lock] heartbeat ok (device=%s)", identity.DeviceName)
			}
		case <-syncTicker.C:
			if err := runSyncPass(ctx, engine); err != nil {
				if errors.Is(err, ErrLockLost) {
					log.Println("[lock] another device took over the workspace lock — sync paused automatically.")
					return nil
				}
				log.Printf("[sync] pass error: %v", err)
			}
		}
	}
}

func runSyncPass(ctx context.Context, engine *Engine) error {
	stats, err := engine.SyncOnce(ctx)
	if err != nil {
		return err
	}
	if stats.Pushed > 0 || stats.Pulled > 0 || stats.Deleted > 0 || stats.Conflicts > 0 {
		log.Printf("[sync] cycle complete: pushed=%d pulled=%d deleted=%d conflicts=%d",
			stats.Pushed, stats.Pulled, stats.Deleted, stats.Conflicts)
	}
	return nil
}

func acquireLockInteractive(ctx context.Context, client *Client, id *DeviceIdentity, force bool) (*AcquireResponse, error) {
	res, err := client.AcquireLock(ctx, id.DeviceID, id.DeviceName, id.OS, force)
	if err == nil {
		return res, nil
	}

	var conflictErr *LockConflictError
	if !errors.As(err, &conflictErr) {
		return nil, err
	}

	holder := "unknown device"
	if conflictErr.LockedBy != nil {
		holder = fmt.Sprintf("%s (%s, last active %s)", conflictErr.LockedBy.DeviceName, conflictErr.LockedBy.OS, conflictErr.LockedBy.LastHeartbeat.Format(time.RFC3339))
	}
	fmt.Printf("\n[Active Device Lock] 云端工作区当前正由另一台设备同步中：%s\n", holder)
	fmt.Print("是否接管同步权？（旧设备将自动暂停，本机将先拉取云端最新代码）[y/N]: ")

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer != "y" && answer != "yes" {
		return nil, errors.New("takeover cancelled by user")
	}

	return client.AcquireLock(ctx, id.DeviceID, id.DeviceName, id.OS, true)
}

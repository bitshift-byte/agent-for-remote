package syncer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type RunOptions struct {
	ServerURL  string
	Username   string
	Secret     string
	LocalDir   string
	Mounts     []MountConfig
	DeviceName string
	Interval   time.Duration
	Force      bool
}

func RunDaemon(ctx context.Context, opts RunOptions) error {
	if opts.Interval <= 0 {
		opts.Interval = 800 * time.Millisecond
	}
	if len(opts.Mounts) > 0 {
		return runMultiMountDaemon(ctx, opts)
	}

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	identity, err := LoadOrCreateDeviceIdentity(opts.LocalDir, opts.DeviceName)
	if err != nil {
		return fmt.Errorf("load device identity: %w", err)
	}

	client := NewClient(opts.ServerURL)
	if _, err := client.Login(runCtx, opts.Username, opts.Secret); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}
	log.Printf("[auth] logged in as %s on %s", opts.Username, opts.ServerURL)

	acq, err := acquireLockInteractive(runCtx, client, identity, opts.Force)
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
		stats, err := engine.InitialTakeoverPull(runCtx)
		if err != nil {
			return fmt.Errorf("initial takeover pull: %w", err)
		}
		log.Printf("[sync] takeover pull complete (pulled=%d)", stats.Pulled)
	} else {
		log.Printf("[lock] active device lock acquired (device=%s, epoch=%d)", identity.DeviceName, acq.Lock.Epoch)
	}

	log.Printf("[sync] watching %s <-> %s/workspace (interval=%s, ignoring node_modules/.venv/target/.git)", opts.LocalDir, opts.ServerURL, opts.Interval)

	var syncMu sync.Mutex

	syncMu.Lock()
	initialErr := runSyncPass(runCtx, engine)
	syncMu.Unlock()
	if initialErr != nil {
		if errors.Is(initialErr, ErrLockLost) {
			log.Println("[lock] another device took over the workspace lock — sync paused automatically.")
			return nil
		}
		if errors.Is(initialErr, ErrMassDeleteBlocked) {
			log.Printf("[sync] ⛔ %v", initialErr)
			return initialErr
		}
		log.Printf("[sync] initial pass error: %v", initialErr)
	} else {
		log.Println("[sync] initial sync check complete — watching for changes...")
	}

	go RunExecLoop(runCtx, client, engine, &syncMu, func() {
		log.Println("[lock] another device took over the workspace lock — pausing local exec & sync.")
		cancelRun()
	})
	go RunTunnelLoop(runCtx, client, func() {
		log.Println("[lock] another device took over the workspace lock — pausing local preview tunnel.")
		cancelRun()
	})

	syncTicker := time.NewTicker(opts.Interval)
	hbTicker := time.NewTicker(15 * time.Second)
	defer syncTicker.Stop()
	defer hbTicker.Stop()

	for {
		select {
		case <-runCtx.Done():
			log.Println("[sync] shutting down...")
			return nil
		case <-hbTicker.C:
			if err := client.Heartbeat(runCtx); err != nil {
				if errors.Is(err, ErrLockLost) {
					log.Println("[lock] another device took over the workspace lock — sync paused automatically.")
					return nil
				}
				log.Printf("[lock] heartbeat warning: %v", err)
			}
		case <-syncTicker.C:
			syncMu.Lock()
			passErr := runSyncPass(runCtx, engine)
			syncMu.Unlock()
			if passErr != nil {
				if errors.Is(passErr, ErrLockLost) {
					log.Println("[lock] another device took over the workspace lock — sync paused automatically.")
					return nil
				}
				if errors.Is(passErr, ErrMassDeleteBlocked) {
					log.Printf("[sync] ⛔ %v", passErr)
					return passErr
				}
				log.Printf("[sync] pass error: %v", passErr)
			}
		}
	}
}

func runMultiMountDaemon(ctx context.Context, opts RunOptions) error {
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	idDir := opts.LocalDir
	if idDir == "" && len(opts.Mounts) > 0 {
		idDir = opts.Mounts[0].Dir
	}
	identity, err := LoadOrCreateDeviceIdentity(idDir, opts.DeviceName)
	if err != nil {
		return fmt.Errorf("load device identity: %w", err)
	}

	baseClient := NewClient(opts.ServerURL)
	if _, err := baseClient.Login(runCtx, opts.Username, opts.Secret); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}
	log.Printf("[auth] logged in as %s on %s (device=%s, mounts=%d)", opts.Username, opts.ServerURL, identity.DeviceName, len(opts.Mounts))

	type activeMount struct {
		mountPath string
		client    *Client
		me        *MountEngine
	}

	active := make([]*activeMount, 0, len(opts.Mounts))
	mountMap := make(map[string]*MountEngine)
	var defaultMount *MountEngine

	for _, mc := range opts.Mounts {
		if mc.Enabled != nil && !*mc.Enabled {
			continue
		}
		dir := strings.TrimSpace(mc.Dir)
		if dir == "" {
			continue
		}
		repoName := strings.TrimSpace(mc.Name)
		if repoName == "" {
			repoName = filepath.Base(dir)
		}
		mountPath := BuildMountPath(identity.DeviceName, repoName)
		mcClient := baseClient.CloneForMount(mountPath)
		acq, err := acquireLockInteractive(runCtx, mcClient, identity, opts.Force)
		if err != nil {
			return fmt.Errorf("acquire lock for %s: %w", mountPath, err)
		}
		eng, err := NewEngine(dir, mcClient)
		if err != nil {
			return fmt.Errorf("init sync engine for %s: %w", mountPath, err)
		}
		if acq.TakenOver {
			if _, err := eng.InitialTakeoverPull(runCtx); err != nil {
				return fmt.Errorf("takeover pull for %s: %w", mountPath, err)
			}
		}
		me := &MountEngine{Mount: mountPath, Engine: eng, Mu: &sync.Mutex{}}
		am := &activeMount{mountPath: mountPath, client: mcClient, me: me}
		active = append(active, am)
		mountMap[mountPath] = me
		if defaultMount == nil {
			defaultMount = me
		}
		log.Printf("[sync] mounted %s <-> /workspace/%s", dir, mountPath)
	}

	if len(active) == 0 {
		return errors.New("no enabled mounts configured")
	}

	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		for _, am := range active {
			_ = am.client.ReleaseLock(releaseCtx)
		}
		log.Printf("[lock] released %d mount locks", len(active))
	}()

	// Initial sync pass for every mount before going online for exec
	for _, am := range active {
		am.me.Mu.Lock()
		err := runSyncPass(runCtx, am.me.Engine)
		am.me.Mu.Unlock()
		if err != nil {
			log.Printf("[sync:%s] initial pass warning: %v", am.mountPath, err)
		}
	}

	// Primary client (uses first mount's active lease token for device-level exec & tunnel channels)
	primaryClient := active[0].client
	go RunMultiExecLoop(runCtx, primaryClient, identity.DeviceName, mountMap, defaultMount, nil, func() {
		log.Println("[lock] mount lock lost — pausing local exec.")
		cancelRun()
	})
	go RunTunnelLoop(runCtx, primaryClient, func() {
		log.Println("[lock] mount lock lost — pausing local preview tunnel.")
		cancelRun()
	})

	var wg sync.WaitGroup
	for _, am := range active {
		wg.Add(1)
		go func(m *activeMount) {
			defer wg.Done()
			syncTicker := time.NewTicker(opts.Interval)
			hbTicker := time.NewTicker(15 * time.Second)
			defer syncTicker.Stop()
			defer hbTicker.Stop()
			for {
				select {
				case <-runCtx.Done():
					return
				case <-hbTicker.C:
					if err := m.client.Heartbeat(runCtx); err != nil {
						log.Printf("[lock:%s] heartbeat warning: %v", m.mountPath, err)
					}
				case <-syncTicker.C:
					m.me.Mu.Lock()
					err := runSyncPass(runCtx, m.me.Engine)
					m.me.Mu.Unlock()
					if err != nil {
						log.Printf("[sync:%s] pass error: %v", m.mountPath, err)
					}
				}
			}
		}(am)
	}

	<-runCtx.Done()
	wg.Wait()
	return nil
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

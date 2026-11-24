package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"super-agent/gateway/internal/auth"
	"super-agent/gateway/internal/config"
	"super-agent/gateway/internal/docker"
	"super-agent/gateway/internal/execbridge"
	"super-agent/gateway/internal/incus"
	"super-agent/gateway/internal/proxy"
	"super-agent/gateway/internal/runtime"
	"super-agent/gateway/internal/server"
	"super-agent/gateway/internal/syncsvc"
	"super-agent/gateway/internal/tunnel"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	authMgr := auth.NewManager(cfg.JWTSecret, cfg.JWTTTL)
	// Internal desktop-to-gateway token manager with 7-day validity for long-lived sessions
	internalAuthMgr := auth.NewManager(cfg.JWTSecret, 7*24*time.Hour)

	var desktopMgr runtime.Provider
	if cfg.DesktopRuntime == "incus" {
		incusClient := incus.NewClient(cfg.IncusSocket)
		pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := incusClient.Ping(pingCtx); err != nil {
			log.Printf("[warn] incus daemon not reachable at %s: %v (will retry on demand)", cfg.IncusSocket, err)
		} else {
			log.Printf("[ok] connected to incus KVM daemon at %s (image=%s, network=%s)", cfg.IncusSocket, cfg.IncusVMImage, cfg.IncusNetwork)
		}
		cancel()
		desktopMgr = incus.NewManager(incusClient, cfg, internalAuthMgr.Sign)
	} else {
		dockerClient, err := docker.NewClient(cfg.DockerHost)
		if err != nil {
			log.Fatalf("failed to initialize docker client: %v", err)
		}
		pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := dockerClient.Ping(pingCtx); err != nil {
			log.Printf("[warn] docker daemon not reachable at %s: %v (will retry on demand)", cfg.DockerHost, err)
		} else {
			log.Printf("[ok] connected to docker daemon at %s", cfg.DockerHost)
		}
		cancel()
		desktopMgr = docker.NewManager(dockerClient, cfg, internalAuthMgr.Sign)
	}

	vncProxy := proxy.NewVNCProxy(desktopMgr)
	lockMgr := syncsvc.NewLockManager(cfg.SyncLockTTL)
	syncSvc := syncsvc.NewService(cfg.WorkspaceDataDir, lockMgr)
	execBroker := execbridge.NewBroker()
	tunnelBroker := tunnel.NewBroker()
	devices := syncsvc.NewDeviceRegistry(cfg.WorkspaceDataDir)
	srv := server.New(cfg, authMgr, desktopMgr, vncProxy, syncSvc, execBroker, tunnelBroker, devices)

	reaperCtx, cancelReaper := context.WithCancel(context.Background())
	defer cancelReaper()
	go desktopMgr.StartIdleReaper(reaperCtx)

	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("Super-Agent Gateway listening on :%s (image=%s, network=%s)", cfg.Port, cfg.DesktopImage, cfg.DockerNetwork)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server error: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("shutting down gateway...")
	ctx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(ctx)
}

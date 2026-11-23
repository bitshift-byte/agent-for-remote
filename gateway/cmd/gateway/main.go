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
	"super-agent/gateway/internal/proxy"
	"super-agent/gateway/internal/server"
	"super-agent/gateway/internal/syncsvc"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

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

	authMgr := auth.NewManager(cfg.JWTSecret, cfg.JWTTTL)
	// Internal desktop-to-gateway token manager with 7-day validity for long-lived container sessions
	internalAuthMgr := auth.NewManager(cfg.JWTSecret, 7*24*time.Hour)
	dockerMgr := docker.NewManager(dockerClient, cfg, internalAuthMgr.Sign)
	vncProxy := proxy.NewVNCProxy(dockerMgr)
	lockMgr := syncsvc.NewLockManager(cfg.SyncLockTTL)
	syncSvc := syncsvc.NewService(cfg.WorkspaceDataDir, lockMgr)
	execBroker := execbridge.NewBroker()
	devices := syncsvc.NewDeviceRegistry(cfg.WorkspaceDataDir)
	srv := server.New(cfg, authMgr, dockerMgr, vncProxy, syncSvc, execBroker, devices)

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

package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Golangcodes/nextdeploy/daemon/internal/config"
	"github.com/Golangcodes/nextdeploy/daemon/internal/logging"
	"github.com/Golangcodes/nextdeploy/daemon/internal/types"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/updater"
)

const systemctlPath = "/usr/bin/systemctl" // Assuming a default path for systemctl

type NextDeployDaemon struct {
	ctx            context.Context
	cancel         context.CancelFunc
	socketPath     string
	config         *types.DaemonConfig
	socketServer   *SocketServer
	commandHandler *CommandHandler
	logger         *log.Logger
}

func NewNextDeployDaemon(configPath string, socketPathOverride string) (*NextDeployDaemon, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// --socket-path flag from systemd ExecStart takes precedence over config.
	if socketPathOverride != "" {
		cfg.SocketPath = socketPathOverride
	}

	ctx, cancel := context.WithCancel(context.Background())

	logConfig := types.LoggerConfig{
		LogDir:      cfg.LogDir,
		LogFileName: "nextdeployd.log",
		MaxFileSize: 10 * 1024 * 1024,
		MaxBackups:  5,
	}

	logger := logging.SetupLogger(logConfig)
	commandHandler := NewCommandHandler(cfg)
	socketServer := NewSocketServer(cfg, commandHandler)

	return &NextDeployDaemon{
		ctx:            ctx,
		cancel:         cancel,
		socketPath:     configPath,
		config:         cfg,
		socketServer:   socketServer,
		commandHandler: commandHandler,
		logger:         logger,
	}, nil

}

func (d *NextDeployDaemon) Start() error {
	if err := d.socketServer.Start(); err != nil {
		return fmt.Errorf("failed to start socket server: %w", err)
	}
	go d.socketServer.AcceptConnections()
	d.logger.Println("NextDeploy Daemon started successfully")

	// Start background auto-update loop
	go d.startBackgroundUpdateLoop()

	return d.handleSignals()
}

func (d *NextDeployDaemon) startBackgroundUpdateLoop() {
	// Initial check after 5 minutes
	select {
	case <-time.After(5 * time.Minute):
		d.checkAndUpdate()
	case <-d.ctx.Done():
		return
	}

	// Periodic check every 6 hours
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			d.checkAndUpdate()
		case <-d.ctx.Done():
			return
		}
	}
}

func (d *NextDeployDaemon) checkAndUpdate() {
	d.logger.Println("[auto-update] Checking for daemon updates...")
	if err := updater.SelfUpdateDaemon(shared.Version); err != nil {
		if strings.Contains(err.Error(), "Already up to date") || strings.Contains(err.Error(), "up to date") {
			d.logger.Println("[auto-update] Daemon is already up to date")
		} else {
			d.logger.Printf("[auto-update] Warning: auto-update failed: %v", err)
		}
	} else {
		d.logger.Println("[auto-update] Update successfully applied. Daemon will restart via systemd.")
	}
}

func (d *NextDeployDaemon) handleSignals() error {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case sig := <-sigChan:
			switch sig {
			case syscall.SIGHUP:
				d.logger.Println("Received SIGHUP, ignoring...")
			case syscall.SIGTERM, syscall.SIGINT:
				d.logger.Println("Received interrupt signal, shutting down...")
				d.Shutdown()
				return nil
			}
		case <-d.ctx.Done():
			return nil
		}
	}
}

func (d *NextDeployDaemon) Shutdown() {
	d.logger.Println("Shutting down NextDeploy Daemon...")
	d.cancel()
	_ = d.socketServer.Close()
	d.logger.Println("NextDeploy Daemon shut down gracefully")
}

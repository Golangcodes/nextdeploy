package daemon

import (
	"context"
	"fmt"
	"log"
	"github.com/Golangcodes/nextdeploy/daemon/internal/config"
	"github.com/Golangcodes/nextdeploy/daemon/internal/logging"
	"github.com/Golangcodes/nextdeploy/daemon/internal/types"
	"os"
	"os/signal"
	"syscall"
)

type NextDeployDaemon struct {
	ctx            context.Context
	cancel         context.CancelFunc
	socketPath     string
	config         *types.DaemonConfig
	socketServer   *SocketServer
	commandHandler *CommandHandler
	logger         *log.Logger
}

func NewNextDeployDaemon(socketPath string) (*NextDeployDaemon, error) {
	cfg, err := config.LoadConfig(socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
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
	socketServer := NewSocketServer(cfg.SocketPath, commandHandler)

	return &NextDeployDaemon{
		ctx:            ctx,
		cancel:         cancel,
		socketPath:     socketPath,
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

	return d.handleSignals()
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

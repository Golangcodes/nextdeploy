package daemon

import (
	"context"
	"github.com/Golangcodes/nextdeploy/daemon/internal/types"
	"time"
)

type HealthMonitor struct {
	config        *types.DaemonConfig
	monitoredApps map[string]*MonitoredApp
	ctx           context.Context
	cancel        context.CancelFunc
}

type MonitoredApp struct {
	ContainerName string
	DesiredState  string // "running" or "stopped"
	RestartPolicy string // "always", "on-failure", "never"
	MaxRestarts   int
	RestartCount  int
	LastCheck     time.Time
}

func NewHealthMonitor(config *types.DaemonConfig) *HealthMonitor {
	ctx, cancel := context.WithCancel(context.Background())
	return &HealthMonitor{
		config:        config,
		monitoredApps: make(map[string]*MonitoredApp),
		ctx:           ctx,
		cancel:        cancel,
	}
}

func (hm *HealthMonitor) Start() {
	go hm.monitorLoop()
}

func (hm *HealthMonitor) monitorLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			hm.checkAllContainers()
		case <-hm.ctx.Done():
			return
		}
	}
}

func (hm *HealthMonitor) checkAllContainers() {
	for _, app := range hm.monitoredApps {
		hm.checkContainerHealth(app)
	}
}

func (hm *HealthMonitor) checkContainerHealth(app *MonitoredApp) {
	// Docker logic removed
}
func (hm *HealthMonitor) isApplicationHealthy(containerName string) bool {
	return true
}
func (hm *HealthMonitor) restartContainer(app *MonitoredApp) {
	app.RestartCount++
}
func (app *MonitoredApp) shouldRestart() bool {
	if app.RestartPolicy == "never" {
		return false
	}
	if app.MaxRestarts > 0 && app.RestartCount >= app.MaxRestarts {
		return false
	}
	return true
}

package main

import (
	"context"
	"flag"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"github.com/Battery233/sim-harbor/internal/maintenance"
	"github.com/Battery233/sim-harbor/internal/server"
	"github.com/Battery233/sim-harbor/internal/store"
)

func main() {
	if run() {
		executable, err := os.Executable()
		if err == nil {
			err = syscall.Exec(executable, os.Args, os.Environ())
		}
		if err != nil {
			diagnostics.New("info", 5000, os.Stderr).Record("settings_restart_failed", diagnostics.Fields{})
			os.Exit(1)
		}
	}
}
func run() bool {
	path := flag.String("config", "configs/local.example.json", "runtime configuration")
	flag.Parse()
	c, e := config.LoadManaged(*path)
	if e != nil {
		diagnostics.New("info", 1000, os.Stderr).Record("configuration_rejected", diagnostics.Fields{})
		os.Exit(1)
	}
	lease, e := maintenance.Acquire(c.DataDir)
	if e != nil {
		diagnostics.New("info", 1000, os.Stderr).Record("database_startup_failed", diagnostics.Fields{})
		os.Exit(1)
	}
	defer lease.Close()
	settings, e := config.OpenSettings(c)
	if e != nil {
		diagnostics.New("info", 5000, os.Stderr).Record("configuration_rejected", diagnostics.Fields{})
		os.Exit(1)
	}
	c = settings.Active()
	logs, err := diagnostics.Open(c.DataDir, c.LogLevel, c.DiagnosticsLimit, c.LogMaxBytes, c.LogBackups, os.Stderr, c.LogRetentionDays)
	if err != nil {
		logs = diagnostics.New(c.LogLevel, c.DiagnosticsLimit, os.Stderr)
		logs.Record("logging_startup_failed", diagnostics.Fields{})
	}
	defer logs.Close()
	db, e := store.Open(filepath.Join(c.DataDir, "sim.db"))
	if e != nil {
		logs.Record("database_startup_failed", diagnostics.Fields{})
		os.Exit(1)
	}
	defer db.DB.Close()
	tcp, e := net.Listen("tcp", c.TCPAddr)
	if e != nil {
		logs.Record("tcp_listener_failed", diagnostics.Fields{})
		os.Exit(1)
	}
	defer tcp.Close()
	httpListener, e := net.Listen("tcp", c.HTTPAddr)
	if e != nil {
		logs.Record("http_listener_failed", diagnostics.Fields{})
		os.Exit(1)
	}
	defer httpListener.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	app := server.New(c, db, logs)
	defer app.CloseBackups()
	var restart atomic.Bool
	app.Settings = settings
	app.Restart = func() { restart.Store(true); cancel() }
	httpServer := &http.Server{Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second}
	var wg sync.WaitGroup
	wg.Add(6)
	go func() { defer wg.Done(); app.RunBackups(ctx) }()
	go func() {
		defer wg.Done()
		if app.ServeTCP(ctx, tcp) != nil {
			logs.Record("tcp_listener_failed", diagnostics.Fields{})
			cancel()
		}
	}()
	go func() { defer wg.Done(); logs.RunRetention(ctx) }()
	go func() { defer wg.Done(); app.RunNotifications(ctx) }()
	go func() { defer wg.Done(); app.RunStatusRefresh(ctx) }()
	go func() {
		defer wg.Done()
		if e := httpServer.Serve(httpListener); e != nil && e != http.ErrServerClosed {
			logs.Record("http_listener_failed", diagnostics.Fields{})
			cancel()
		}
	}()
	logs.Record("service_started", diagnostics.Fields{})
	<-ctx.Done()
	shutdown, finish := context.WithTimeout(context.Background(), 5*time.Second)
	defer finish()
	httpServer.Shutdown(shutdown)
	wg.Wait()
	logs.Record("service_stopped", diagnostics.Fields{})
	return restart.Load()
}

package main

import (
	"context"
	"flag"
	"os"
	"time"
	_ "time/tzdata"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/healthcheck"
)

func main() {
	path := flag.String("config", "/config/runtime.json", "runtime configuration")
	flag.Parse()
	c, err := config.LoadManaged(*path)
	if err != nil {
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if healthcheck.Check(ctx, c.HTTPAddr, c.AdminToken) != nil {
		os.Exit(1)
	}
}

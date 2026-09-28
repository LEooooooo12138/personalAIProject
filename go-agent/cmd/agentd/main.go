package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/gateway"

	_ "github.com/yuanleyao/ai-agent/internal/channel/wecom"
)

func main() {
	configPath := flag.String("config", "config/agent.yaml", "path to agent.yaml")
	flag.Parse()

	app, err := core.Bootstrap(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap failed: %v\n", err)
		os.Exit(1)
	}

	app.Server = gateway.NewServerFromApp(app)
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "run failed: %v\n", err)
		os.Exit(1)
	}
}

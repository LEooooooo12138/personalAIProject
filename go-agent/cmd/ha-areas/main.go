// ha-areas is a local metadata-only maintenance command.
// HA_BASE_URL and HA_TOKEN are read from the process environment, never flags.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"io"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"
)

type commandReport struct {
	Mode   string                     `json:"mode"`
	Plan   smarthome.AreaPlan         `json:"plan"`
	Result *smarthome.AreaApplyReport `json:"result,omitempty"`
}

func run(ctx context.Context, args []string, env func(string) string, out io.Writer) int {
	fail := func(message string) int { io.WriteString(out, message+"\n"); return 1 }
	flags := flag.NewFlagSet("ha-areas", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	apply := flags.Bool("apply", false, "explicitly apply metadata assignments")
	inventory := flags.String("inventory", "", "preview a filtered local registry snapshot")
	output := flags.String("output", "", "write JSON report to this local path")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return fail("invalid arguments")
	}
	if *apply && *inventory != "" {
		return fail("offline snapshots support preview only")
	}
	var reg smarthome.RegistrySnapshot
	var client *smarthome.HomeAssistantClient
	if *inventory != "" {
		data, e := os.ReadFile(*inventory)
		if e != nil {
			return fail("inventory unavailable")
		}
		if e = json.Unmarshal(data, &reg); e != nil {
			return fail("invalid inventory")
		}
		// The existing filtered inventory explicitly uses id rather than HA area_id.
		var filtered struct {
			Areas []struct {
				ID string `json:"id"`
			} `json:"areas"`
		}
		if json.Unmarshal(data, &filtered) != nil {
			return fail("invalid inventory")
		}
		for i := range reg.Areas {
			id := filtered.Areas[i].ID
			if reg.Areas[i].ID == "" {
				reg.Areas[i].ID = id
			} else if id != "" && id != reg.Areas[i].ID {
				return fail("conflicting inventory area IDs")
			}
			if reg.Areas[i].ID == "" {
				return fail("invalid inventory area IDs")
			}
		}
	} else {
		endpoint, token := env("HA_BASE_URL"), env("HA_TOKEN")
		u, e := url.Parse(endpoint)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(token) == "" {
			return fail("HA_BASE_URL and HA_TOKEN must contain valid server settings")
		}
		client = smarthome.NewHomeAssistantClient(endpoint, token, 10*time.Second)
		reg, e = client.GetRegistry(ctx)
		if e != nil {
			return fail("registry unavailable")
		}
	}
	report := commandReport{Mode: "preview", Plan: smarthome.BuildAreaPlan(reg)}
	status := 0
	if *apply {
		report.Mode = "apply"
		result, e := smarthome.ApplyAreaPlan(ctx, client, report.Plan)
		report.Result = &result
		if e != nil || result.Failed > 0 || result.Conflict > 0 || result.Unknown > 0 {
			status = 1
		}
	}
	data, e := json.MarshalIndent(report, "", "  ")
	if e != nil {
		return fail("report unavailable")
	}
	data = append(data, '\n')
	if *output != "" {
		if e = os.WriteFile(*output, data, 0600); e != nil {
			return fail("report write failed")
		}
	}
	if _, e = out.Write(data); e != nil {
		return 1
	}
	return status
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout))
}

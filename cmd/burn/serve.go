package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tanrikuluozlem/burn/internal/server"
)

var (
	servePort         int
	serveKubeconfig   string
	serveKubecontext  string
	servePrometheus   string
	servePeriod       string
	serveSnapshot     string
	serveBudgetFile   string
	serveBudgetGap    int
	serveBudgetWindow int
	serveBudgetTrend  int
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start HTTP server for Slack slash commands",
	Long: `Start an HTTP server that responds to Slack slash commands.

Endpoints:
  POST /slack    - Slack slash command handler
  GET  /health   - Health check

Environment variables:
  ANTHROPIC_API_KEY     - Required for AI features
  SLACK_SIGNING_SECRET  - Required for request verification

Example:
  burn serve --port 8080`,
	RunE: runServe,
}

func init() {
	f := serveCmd.Flags()
	f.IntVarP(&servePort, "port", "p", 8080, "port to listen on")
	f.StringVar(&serveKubeconfig, "kubeconfig", "", "path to kubeconfig file")
	f.StringVar(&serveKubecontext, "context", "", "kubeconfig context to use")
	f.StringVar(&servePrometheus, "prometheus", "", "Prometheus server URL")
	f.StringVar(&servePeriod, "period", "", "analysis period (e.g. 1h, 7d, 30d)")
	f.StringVar(&serveSnapshot, "snapshot-path", "", "local snapshot file for /burn budget (or BURN_SNAPSHOT_PATH)")
	f.StringVar(&serveBudgetFile, "budget", "", "path to a JSON budgets file for /burn budget (or BURN_BUDGET_FILE)")
	f.IntVar(&serveBudgetGap, "budget-gap-threshold", 30, "minutes without a snapshot before an interval counts as a gap")
	f.IntVar(&serveBudgetWindow, "budget-window-days", 7, "window length for adjacent-window trend comparison")
	f.IntVar(&serveBudgetTrend, "budget-trend-days", 7, "days of daily means used for the linear trend")

	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	signingSecret := os.Getenv("SLACK_SIGNING_SECRET")
	if signingSecret == "" {
		return fmt.Errorf("SLACK_SIGNING_SECRET not set")
	}

	if servePeriod != "" && !isValidPeriod(servePeriod) {
		return fmt.Errorf("invalid period %q: use Prometheus duration format (e.g. 1h, 7d, 30d)", servePeriod)
	}

	if p := os.Getenv("PROMETHEUS_URL"); p != "" && servePrometheus == "" {
		servePrometheus = p
	}
	if v := os.Getenv("BURN_SNAPSHOT_PATH"); v != "" && serveSnapshot == "" {
		serveSnapshot = v
	}
	if v := os.Getenv("BURN_BUDGET_FILE"); v != "" && serveBudgetFile == "" {
		serveBudgetFile = v
	}

	srv, err := server.New(server.Config{
		Port:          servePort,
		Kubeconfig:    serveKubeconfig,
		Kubecontext:   serveKubecontext,
		PrometheusURL: servePrometheus,
		Period:        servePeriod,
		APIKey:        apiKey,
		SigningSecret: signingSecret,
		SnapshotPath:  serveSnapshot,
		BudgetFile:    serveBudgetFile,
		GapThreshold:  time.Duration(serveBudgetGap) * time.Minute,
		WindowDays:    serveBudgetWindow,
		TrendDays:     serveBudgetTrend,
	})
	if err != nil {
		return err
	}

	// Graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("Starting server on :%d", servePort)
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-stop
	log.Println("Shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return srv.Shutdown(ctx)
}

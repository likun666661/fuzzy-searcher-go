package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/likun666661/youtu-rag-service/internal/config"
	"github.com/likun666661/youtu-rag-service/internal/svc"
)

func main() {
	checkConfig := flag.Bool("check-config", false, "validate service configuration and print a JSON report")
	flag.Parse()

	cfg := config.Load()
	if *checkConfig {
		report := config.Validate(cfg)
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			log.Fatalf("encode config validation report: %v", err)
		}
		if !report.Ready {
			os.Exit(2)
		}
		return
	}
	if cfg.ValidateOnStart {
		report := config.Validate(cfg)
		if err := report.Err(); err != nil {
			log.Fatalf("%v", err)
		}
	}
	service := svc.NewService(cfg)
	server := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: service.Routes(),
	}

	go func() {
		log.Printf("starting %s on %s", cfg.AppName, cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server failed: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("server shutdown failed: %v", err)
	}
	log.Printf("server stopped")
}

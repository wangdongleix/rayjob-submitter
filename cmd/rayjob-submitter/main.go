package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/verl-project/rayjob-submitter/internal/api"
	"github.com/verl-project/rayjob-submitter/internal/config"
	kube "github.com/verl-project/rayjob-submitter/internal/kubernetes"
	"github.com/verl-project/rayjob-submitter/internal/rayjob"
)

func main() {
	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	typedClient, dynamicClient, err := kube.NewClients()
	if err != nil {
		log.Fatalf("create kubernetes clients: %v", err)
	}
	log.Printf("checking kubernetes permissions in namespace %s", cfg.TargetNamespace)
	checkCtx, cancel := context.WithTimeout(rootCtx, 10*time.Second)
	defer cancel()
	if err := kube.CheckPermissions(checkCtx, typedClient, cfg.TargetNamespace); err != nil {
		log.Fatalf("check kubernetes permissions: %v", err)
	}
	log.Printf("kubernetes permission check passed")
	template, err := rayjob.LoadRayJobTemplate(cfg.RayJobTemplatePath)
	if err != nil {
		log.Fatalf("load RayJob template: %v", err)
	}

	app := api.NewServer(cfg, typedClient, dynamicClient, template)
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go app.RunStaleRayJobCleanup(rootCtx)

	go func() {
		log.Printf("rayjob-submitter listening on %s", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	<-rootCtx.Done()
	stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}

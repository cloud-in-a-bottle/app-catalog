package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/imbue-openhost/openhost-catalog/internal/catalog"
	"github.com/imbue-openhost/openhost-catalog/internal/config"
	"github.com/imbue-openhost/openhost-catalog/internal/orgrename"
	"github.com/imbue-openhost/openhost-catalog/internal/store"
	"github.com/imbue-openhost/openhost-catalog/internal/web"
)

func main() {
	cfg := config.Load()

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if err := st.Init(context.Background()); err != nil {
		log.Fatalf("initialize store schema: %v", err)
	}

	reconcileSourceURLs(st)
	seedDefaultSource(cfg, st)

	handler, err := web.NewServer(cfg, st)
	if err != nil {
		log.Fatalf("initialize web server: %v", err)
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("openhost-catalog listening on %s", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server failed: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
}

// reconcileSourceURLs moves any persisted feed URL off a renamed repository or
// owner. Runs before seedDefaultSource so a rewritten source still counts as an
// existing source and does not get duplicated.
//
// Not gated on orgrename.Enabled(): the repository move (openhost-apps ->
// app-manifest) has already happened and its new path resolves now, while the
// owner move stays gated inside orgrename.Rewrite. Idempotent, and never
// fatal -- a stale URL still resolves through GitHub's redirect, so failing to
// rewrite is a risk to close, not a reason to refuse to start.
func reconcileSourceURLs(st *store.Store) {
	ctx := context.Background()
	sources, err := st.ListSources(ctx)
	if err != nil {
		log.Printf("orgrename: failed to list sources: %v", err)
		return
	}
	for _, src := range sources {
		rewritten, changed := orgrename.Rewrite(src.URL)
		if !changed {
			continue
		}
		if err := st.SetSourceURL(ctx, src.ID, rewritten); err != nil {
			log.Printf("orgrename: failed to repoint source %s: %v", src.ID, err)
			continue
		}
		log.Printf("orgrename: source %s repointed %s -> %s", src.ID, src.URL, rewritten)
	}
}

// seedDefaultSource adds and syncs the default catalog source if no sources
// exist yet and DEFAULT_SOURCE_URL is configured.
func seedDefaultSource(cfg config.Config, st *store.Store) {
	if cfg.DefaultSourceURL == "" {
		return
	}

	ctx := context.Background()
	sources, err := st.ListSources(ctx)
	if err != nil {
		log.Printf("seed: failed to list sources: %v", err)
		return
	}
	if len(sources) > 0 {
		return
	}

	log.Printf("seed: no sources configured, adding default source: %s", cfg.DefaultSourceURL)
	src := store.Source{
		ID:      "official",
		Name:    "OpenHost Official",
		URL:     cfg.DefaultSourceURL,
		Enabled: true,
	}
	if err := st.CreateSource(ctx, src); err != nil {
		log.Printf("seed: failed to create default source: %v", err)
		return
	}

	svc := catalog.NewService(st, &http.Client{Timeout: cfg.RequestTimeout})
	if err := svc.SyncSource(ctx, src.ID); err != nil {
		log.Printf("seed: failed to sync default source: %v", err)
		return
	}
	log.Printf("seed: default source synced successfully")
}

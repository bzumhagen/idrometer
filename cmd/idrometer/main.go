// Command idrometer is the composition root: it builds the adapters, injects
// them into the domain services and runs the requested subcommand.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bzumhagen/idrometer/internal/api"
	"github.com/bzumhagen/idrometer/internal/api/ingest"
	"github.com/bzumhagen/idrometer/internal/api/ingest/ispindel"
	"github.com/bzumhagen/idrometer/internal/backup"
	"github.com/bzumhagen/idrometer/internal/config"
	"github.com/bzumhagen/idrometer/internal/crypto/argon2"
	"github.com/bzumhagen/idrometer/internal/domain"
	"github.com/bzumhagen/idrometer/internal/events"
	"github.com/bzumhagen/idrometer/internal/store/sqlite"
	"github.com/bzumhagen/idrometer/internal/web"
	"golang.org/x/term"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "idrometer:", err)
		os.Exit(1)
	}
}

const usage = `usage: idrometer <command>

commands:
  serve            run the server
  admin set-password   set the admin password (prompts, or reads one line from stdin)
  backup --out F   write a consistent snapshot of the database to F
  version          print the version`

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "serve":
		return serve()
	case "backup":
		return backupCmd(args[1:])
	case "admin":
		return admin(args[1:], stdout)
	case "version":
		fmt.Fprintln(stdout, version)
		return nil
	case "-h", "--help", "help":
		fmt.Fprintln(stdout, usage)
		return nil
	}
	return fmt.Errorf("unknown command %q\n%s", args[0], usage)
}

func newLogger(cfg config.Config) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
}

func serve() error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	if err := cfg.ValidateServe(); err != nil {
		return err
	}
	log := newLogger(cfg)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := sqlite.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	clock := domain.SystemClock{}
	broker := events.NewBroker()
	ingestor := domain.NewIngestor(store, clock, broker)
	sweeper := domain.NewSweeper(store, clock, broker, log)
	queries := domain.NewQueries(store, clock)
	auth, err := domain.NewAuth(store, clock, argon2.New())
	if err != nil {
		return err
	}
	shutdown := make(chan struct{})
	ui, err := web.New(web.Services{
		Queries: queries, Editor: domain.NewEditor(store, clock, broker), Auth: auth,
		Prefs: domain.NewPreferenceService(store, broker), Events: broker, Clock: clock, Log: log,
		SecureCookies: cfg.SecureCookies, IngestToken: cfg.IngestToken, IngestListen: cfg.Listen, Done: shutdown,
	})
	if err != nil {
		return err
	}
	if has, err := auth.HasPassword(ctx); err == nil && !has {
		log.Warn("no admin password set; the UI is read-only until you run: idrometer admin set-password")
	}

	handler := api.NewRouter(api.Deps{
		Ingest: ingest.NewHandler(ingestor, cfg.IngestToken, log, ispindel.Decoder{}),
		Health: store.Ping,
		Log:    log,
		Mount:  []func(*http.ServeMux){api.MountFermentations(queries, log), ui.Mount},
	})
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// No WriteTimeout: SSE streams are long-lived; stream handlers clear
		// both the read and write deadlines.
	}

	srv.RegisterOnShutdown(func() { close(shutdown) }) // ends open SSE streams
	// Background jobs must finish before the deferred store.Close runs.
	var wg sync.WaitGroup
	wg.Go(func() { sweeper.Run(ctx, cfg.SweepInterval) })
	wg.Go(func() {
		(&backup.Service{
			Snap: store, Dir: cfg.BackupDir, Interval: cfg.BackupInterval, Keep: cfg.BackupKeep, Log: log,
		}).Run(ctx)
	})

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Listen, "db", cfg.DBPath, "version", version)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	stop() // restore default signal handling: a second Ctrl-C kills the process
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	wg.Wait()
	return err
}

func backupCmd(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	out := fs.String("out", "", "destination file (must not exist)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("backup: --out is required")
	}
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	ctx := context.Background()
	store, err := sqlite.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()
	return store.Snapshot(ctx, *out)
}

func admin(args []string, stdout io.Writer) error {
	if len(args) != 1 || args[0] != "set-password" {
		return errors.New("usage: idrometer admin set-password")
	}
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	pw, err := readPassword(stdout)
	if err != nil {
		return err
	}
	ctx := context.Background()
	store, err := sqlite.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()
	auth, err := domain.NewAuth(store, domain.SystemClock{}, argon2.New())
	if err != nil {
		return err
	}
	if err := auth.SetPassword(ctx, pw); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Admin password set. Existing sessions were signed out.")
	return nil
}

// readPassword prompts twice on a terminal, or reads a single line from a pipe
// (for install scripts).
func readPassword(stdout io.Writer) (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(stdout, "New admin password: ")
		a, err := term.ReadPassword(fd)
		fmt.Fprintln(stdout)
		if err != nil {
			return "", err
		}
		fmt.Fprint(stdout, "Repeat password: ")
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(stdout)
		if err != nil {
			return "", err
		}
		if string(a) != string(b) {
			return "", errors.New("passwords do not match")
		}
		return string(a), nil
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no password supplied on stdin")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

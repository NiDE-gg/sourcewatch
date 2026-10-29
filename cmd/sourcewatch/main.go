package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/kritzerenkrieg/sourcewatch/internal/app"
	"github.com/kritzerenkrieg/sourcewatch/internal/bot"
	"github.com/kritzerenkrieg/sourcewatch/internal/config"
	"github.com/kritzerenkrieg/sourcewatch/internal/notify"
	"github.com/kritzerenkrieg/sourcewatch/internal/poller"
	"github.com/kritzerenkrieg/sourcewatch/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("sourcewatch stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log.Info("config loaded", "config", cfg, "database", cfg.Database)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(cfg.Database)
	if err != nil {
		return err
	}
	defer st.Close()

	pipe := &app.Pipeline{Store: st, Log: log, Ctx: ctx}
	var discord *notify.Discord
	if cfg.DiscordToken != "" {
		discord, err = notify.NewDiscord(cfg.DiscordToken)
		if err != nil {
			return err
		}
		pipe.Notifier = discord
	}

	p := poller.New(pipe.Handle)
	if !cfg.DisableGameTracker {
		p.Fallback = poller.NewGameTracker() // used only when A2S gets no answer (B5)
	}
	mgr := poller.NewManager(ctx, p)
	for _, addr := range cfg.Servers {
		mgr.Watch(addr)
	}
	// Servers people asked the bot to watch come back after a restart.
	addrs, err := st.AllListenerAddrs()
	if err != nil {
		return err
	}
	for _, addr := range addrs {
		mgr.Watch(addr)
	}

	var botErr error
	if cfg.DiscordToken != "" {
		b, err := bot.New(cfg.DiscordToken, cfg.ApplicationID, cfg.GuildID, log, bot.Deps{Store: st, Watcher: mgr, DM: discord})
		if err != nil {
			return err
		}
		botErr = b.Run(ctx) // blocks until ctx is done or the connection fails
		stop()
	} else {
		log.Warn("no discord token: polling and storing only")
		<-ctx.Done()
	}
	mgr.Wait()
	return botErr
}

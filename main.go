package main

import (
	"Brainy/ai"
	"Brainy/bot"
	"Brainy/core"
	"Brainy/lib/sl"
	"Brainy/storage"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

const (
	envLocal = "local"
	envDev   = "dev"
	envProd  = "prod"
)

func main() {

	configPath := flag.String("conf", "config.yml", "path to config file")
	flag.Parse()

	conf := core.MustLoad(*configPath)
	log := setupLogger(conf.Env)
	log.With(
		slog.String("config", *configPath),
		slog.String("env", conf.Env),
		slog.String("model", conf.Model),
	).Info("starting brainy bot")

	// Initialize storage based on config
	var store storage.ContextStorage
	if conf.Mongo.Enabled {
		mongoURI := fmt.Sprintf("mongodb://%s:%s@%s:%s",
			conf.Mongo.User, conf.Mongo.Password,
			conf.Mongo.Host, conf.Mongo.Port)
		var err error
		store, err = storage.NewMongoStorage(mongoURI, conf.Mongo.Database, log)
		if err != nil {
			log.With(
				slog.String("db", conf.Mongo.Database),
				slog.String("user", conf.Mongo.User),
				slog.String("host", conf.Mongo.Host),
			).Error("falling back to memory", sl.Err(err))
			store = storage.NewMemoryStorage()
		} else {
			log.Info("using MongoDB storage")
		}
	} else {
		store = storage.NewMemoryStorage()
		log.Info("using in-memory storage")
	}

	chat := ai.NewChat(conf, log, store)
	tgBot, err := bot.NewTgBot(conf, log)
	if err != nil {
		log.Error("creating telegram", sl.Err(err))
		return
	}

	tgBot.SetChat(chat)

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start bot in goroutine
	go func() {
		if err := tgBot.Start(); err != nil {
			log.Error("bot stopped with error", sl.Err(err))
		}
	}()

	log.Info("bot started")

	// Wait for shutdown signal
	sig := <-sigChan
	log.Info("received signal, shutting down", slog.String("signal", sig.String()))

	// Graceful shutdown
	tgBot.Stop()

	// Close storage connection
	if err := chat.Close(); err != nil {
		log.Error("error closing chat service", sl.Err(err))
	}

	log.Info("shutdown complete")
}

func setupLogger(env string) *slog.Logger {
	var log *slog.Logger

	switch env {
	case envLocal:
		log = slog.New(
			slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}),
		)
	case envDev:
		log = slog.New(
			slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}),
		)
	case envProd:
		log = slog.New(
			slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}),
		)
	}

	return log
}

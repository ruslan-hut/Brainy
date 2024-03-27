package main

import (
	"Brainy/ai"
	"Brainy/bot"
	"Brainy/core"
	"Brainy/lib/sl"
	"flag"
	"log/slog"
	"os"
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
	log.Info("starting brainy bot", slog.String("config", *configPath), slog.String("env", conf.Env))

	chat := ai.NewChat(conf, log)
	tgBot, err := bot.NewTgBot(conf, log)
	if err != nil {
		log.Error("creating telegram", sl.Err(err))
		return
	}

	tgBot.SetChat(chat)
	err = tgBot.Start()
	if err != nil {
		log.Error("starting telegram", sl.Err(err))
		return
	}
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

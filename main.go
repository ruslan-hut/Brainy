package main

import (
	"Brainy/ai"
	"Brainy/bot"
	"Brainy/core"
	"flag"
	"log"
)

func main() {

	configPath := flag.String("conf", "config.yml", "path to config file")
	flag.Parse()

	log.Println("using config file: " + *configPath)
	conf, err := core.GetConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	chat := ai.NewChat(conf)
	tgBot, err := bot.NewTgBot(conf)
	if err != nil {
		log.Fatal(err)
	}
	tgBot.SetChat(chat)
	tgBot.Start()
}

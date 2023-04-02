package main

import (
	"Brainy/ai"
	"Brainy/bot"
	"Brainy/core"
	"log"
)

func main() {
	conf, err := core.GetConfig()
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

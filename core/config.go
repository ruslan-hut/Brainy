package core

import (
	"fmt"
	"github.com/ilyakaznacheev/cleanenv"
	"log"
	"sync"
)

type Config struct {
	Env            string `yaml:"env" env-default:"local"`
	TelegramApiKey string `yaml:"telegram_api_key" env-default:""`
	OpenAIApiKey   string `yaml:"openai_api_key" env-default:""`
	Username       string `yaml:"username" env-default:""`
	Mongo          struct {
		Enabled  bool   `yaml:"enabled" env-default:"false"`
		Host     string `yaml:"host" env-default:"127.0.0.1"`
		Port     string `yaml:"port" env-default:"27017"`
		User     string `yaml:"user" env-default:"admin"`
		Password string `yaml:"password" env-default:"pass"`
		Database string `yaml:"database" env-default:""`
	}
}

var instance *Config
var once sync.Once

func MustLoad(path string) *Config {
	var err error
	once.Do(func() {
		instance = &Config{}
		if err = cleanenv.ReadConfig(path, instance); err != nil {
			desc, _ := cleanenv.GetDescription(instance, nil)
			err = fmt.Errorf("config: %s; %s", err, desc)
			instance = nil
			log.Fatal(err)
		}
	})
	return instance
}

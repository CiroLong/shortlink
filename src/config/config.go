package config

import (
	"fmt"
	"github.com/spf13/viper"
)

var c *Config

type Config struct {
	Mysql MysqlConfig `mapstructure:"mysql"`
	Redis RedisConfig `mapstructure:"redis"`
}

type MysqlConfig struct {
	Dsn string `mapstructure:"dsn"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

func LoadConfig() *Config {
	viper.SetConfigName("app")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./config/")
	if err := viper.ReadInConfig(); err != nil {
		fmt.Println("err", err.Error())
		panic("failed to read config file")
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		panic("failed to unmarshal config")
	}
	c = &config
	return &config
}

func GetConfig() *Config {
	return c
}

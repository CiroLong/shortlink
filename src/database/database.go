package database

import (
	"context"
	"github.com/go-redis/redis/v8"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"shortlink/src/config"
	"time"
)

type DB struct {
	MySql *gorm.DB
	Redis *redis.Client
	Ctx   context.Context
}

var db *DB

func GetDB() *DB {
	return db
}

const CacheDuration = 4 * time.Hour

func init() {
	db = &DB{
		Ctx: context.Background(),
	}
}
func InitRedis() {

	c := config.GetConfig()
	redis := redis.NewClient(&redis.Options{
		Addr:     c.Redis.Addr,
		Password: c.Redis.Password,
		DB:       c.Redis.DB,
	})
	_, err := redis.Ping(db.Ctx).Result()
	if err != nil {
		panic("failed to connect redis")
	}
	db.Redis = redis
}

func InitDB() {
	dsn := config.GetConfig().Mysql.Dsn
	mysql, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		panic("failed to connect database")
	}
	db.MySql = mysql
}

package service

import (
	"errors"
	"fmt"
	"github.com/CiroLong/shortlink/src/database"
	"github.com/go-redis/redis/v8"
	"time"
)

type Link struct {
	ShortURL    string    `gorm:"short_url;primary_key" json:"short_url"`
	OriginalUrl string    `gorm:"original_url" json:"original_url"`
	VisitCount  uint      `gorm:"visit_count" json:"visit_count"`
	ExpireAt    time.Time `gorm:"expire_at" json:"expire_at"`
}

// AutoMigrate performs automatic database migrations for the Link model.
func AutoMigrate() {
	db := database.GetDB()
	db.MySql.AutoMigrate(&Link{})
}

// AddToBloomFilter add to bloom filter
func AddToBloomFilter(key string) error {
	db := database.GetDB()
	return db.Redis.Do(db.Ctx, "BF.ADD", "bloom:shortlink", key).Err()
}

func SaveUrlMapping(shortURL string, longURL string, id string) error {
	db := database.GetDB()
	err := db.Redis.Set(db.Ctx, shortURL, longURL, database.CacheDuration).Err()
	if err != nil {
		return err
	}

	go func() {
		_ = AddToBloomFilter(shortURL)

		link := Link{
			ShortURL:    shortURL,
			OriginalUrl: longURL,
			VisitCount:  0,
			ExpireAt:    time.Now().Add(database.CacheDuration * 2),
		}
		err := db.MySql.Save(&link).Error
		if err != nil {
			fmt.Println("mysql write error:", err.Error())
		}
	}()

	return nil
}

// RetrieveInitialUrl 通过短链获取长链
func RetrieveInitialUrl(shortURL string) (string, error) {
	db := database.GetDB()
	result, err := db.Redis.Get(db.Ctx, shortURL).Result()
	var link Link
	if errors.Is(err, redis.Nil) {
		if err := db.MySql.First(&link, shortURL).Error; err != nil {
			return "", err
		}
		if link.ExpireAt.Before(time.Now()) {
			return "", errors.New("link expired")
		}

		go func() {
			err := db.Redis.Set(db.Ctx, shortURL, link.OriginalUrl, database.CacheDuration).Err()
			if err != nil {
				panic("write redis fail" + err.Error())
			}
		}()

		result = link.OriginalUrl
	} else if err != nil {
		panic(fmt.Sprintf("Failed RetrieveInitialUrl url | Error: %v - shortUrl: %s\n", err, shortURL))
	}

	go func() {
		redisKey := fmt.Sprintf("visit:%s", shortURL)
		db.Redis.Incr(db.Ctx, redisKey)
	}()
	return result, nil
}

package service

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/go-redis/redis/v8"
	"github.com/itchyny/base58-go"
	"math/big"
	"os"
	"shortlink/src/database"
)

type Link struct {
	ShortURL    string `gorm:"short_url;primary_key" json:"short_url"`
	OriginalUrl string `gorm:"original_url" json:"original_url"`
	VisitCount  uint   `gorm:"visit_count" json:"visit_count"`
}

func AutoMigrate() {
	db := database.GetDB()
	db.MySql.AutoMigrate(&Link{})
}

func sha256Of(input string) []byte {
	algorithm := sha256.New()
	algorithm.Write([]byte(input))
	return algorithm.Sum(nil)
}

func base58Encoded(bytes []byte) string {
	encoding := base58.BitcoinEncoding
	encoded, err := encoding.Encode(bytes)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
	return string(encoded)
}

func GenerateShortLink(url string, id string) string {
	urlHashBytes := sha256Of(url + id)
	generateNumber := new(big.Int).SetBytes(urlHashBytes).Uint64()
	finalString := base58Encoded([]byte(fmt.Sprintf("%d", generateNumber)))
	return finalString[:8]
}

func SaveUrlMapping(shortURL string, longURL string, id string) error {
	db := database.GetDB()
	err := db.Redis.Set(db.Ctx, shortURL, longURL, database.CacheDuration).Err()
	if err != nil {
		return err
	}

	go func() {
		link := Link{
			ShortURL:    shortURL,
			OriginalUrl: longURL,
		}
		err := db.MySql.Save(&link).Error
		if err != nil {
			fmt.Errorf("mysql write error:", err.Error())
		}
	}()

	return nil
}

// RetrieveInitialUrl 通过短链获取长链
func RetrieveInitialUrl(shortURL string) (string, error) {
	db := database.GetDB()
	result, err := db.Redis.Get(db.Ctx, shortURL).Result()
	if errors.Is(err, redis.Nil) {
		var link Link
		if err := db.MySql.First(&link, shortURL).Error; err != nil {
			return "", err
		}
	} else if err != nil {
		panic(fmt.Sprintf("Failed RetrieveInitialUrl url | Error: %v - shortUrl: %s\n", err, shortURL))
	}

	go func() {
		redisKey := fmt.Sprintf("visit:%s", shortURL)
		db.Redis.Incr(db.Ctx, redisKey)
	}()
	return result, nil
}

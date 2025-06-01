package service

import "github.com/CiroLong/shortlink/src/database"

// AddToBloomFilter add to bloom filter
func AddToBloomFilter(key string) error {
	db := database.GetDB()
	return db.Redis.Do(db.Ctx, "BF.ADD", "bloom:shortlink", key).Err()
}

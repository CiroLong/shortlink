package service

import (
	"fmt"
	"github.com/CiroLong/shortlink/src/database"
	"gorm.io/gorm"
	"log"
	"strings"
	"time"
)

// SyncVisitCounts synchronizes visit counts from Redis to MySQL in batches and based on a threshold.
func SyncVisitCounts() {
	go func() {
		db := database.GetDB()

		batchInterval := time.Hour            // 批量同步间隔
		threshold := int64(3)                 // 阈值
		thresholdInterval := 10 * time.Second // 阈值检测频率

		ticker := time.NewTicker(thresholdInterval)
		defer ticker.Stop()
		mySqlTicker := time.NewTicker(batchInterval)
		defer mySqlTicker.Stop()

		for {
			// 每 xxx 将redis 写入 mysql
			select {
			case <-mySqlTicker.C:
				{
					iter := db.Redis.Scan(db.Ctx, 0, "visit:*", 0).Iterator()
					for iter.Next(db.Ctx) {
						key := iter.Val() // e.g., visit:abc123
						count, _ := db.Redis.Get(db.Ctx, key).Int64()
						code := strings.TrimPrefix(key, "visit:")

						// 写入数据库
						db.MySql.Model(&Link{}).Where("short_url = ?", code).
							UpdateColumn("visit_count", gorm.Expr("visit_count + ?", count))

						// 清除 Redis 记录
						db.Redis.Del(db.Ctx, key)
					}

					//

					now := time.Now().UTC()
					result := db.MySql.
						Where("expire_at IS NOT NULL AND expire_at < ?", now).
						Delete(&Link{})

					if result.Error != nil {
						log.Printf("[Cleaner] 删除过期链接失败: %v", result.Error)
					} else {
						log.Printf("[Cleaner] 清理过期链接成功，共删除 %d 条", result.RowsAffected)
					}
				}

			case <-ticker.C:
				{
					iter := db.Redis.Scan(db.Ctx, 0, "visit:*", 0).Iterator()
					for iter.Next(db.Ctx) {
						key := iter.Val()
						count, err := db.Redis.Get(db.Ctx, key).Int64()
						if err != nil || count < threshold {
							continue
						}
						code := strings.TrimPrefix(key, "visit:")

						// 达到阈值，写入数据库
						err = db.MySql.Model(&Link{}).Where("short_url = ?", code).
							UpdateColumn("visit_count", gorm.Expr("visit_count + ?", count)).Error
						if err == nil {
							db.Redis.Del(db.Ctx, key)
							fmt.Printf("[阈值] 写入 %s, count = %d\n", code, count)
						}
					}
				}
			}
		}
	}()
}

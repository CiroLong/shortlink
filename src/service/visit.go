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

// RebuildBloomFilter 每天定时从数据库加载所有有效链接，重建布隆过滤器
func RebuildBloomFilter() {
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		for {
			// 执行一次
			rebuild()
			// 等待下一个周期
			<-ticker.C
		}
	}()
}

func rebuild() {
	db := database.GetDB()
	redis := db.Redis
	ctx := db.Ctx

	log.Println("开始重建布隆过滤器...")

	// 清空 Bloom Filter
	err := redis.Do(ctx, "DEL", "bloom:shortlink").Err()
	if err != nil {
		log.Println("删除旧布隆过滤器失败:", err)
		return
	}

	// 查出所有未过期的链接
	var links []Link
	now := time.Now()
	err = db.MySql.
		Model(&Link{}).
		Where("expire_at IS NULL OR expire_at > ?", now).
		Find(&links).Error

	if err != nil && err != gorm.ErrRecordNotFound {
		log.Println("查询链接失败:", err)
		return
	}

	// 批量添加
	for _, link := range links {
		err := redis.Do(ctx, "BF.ADD", "bloom:shortlink", link.ShortURL).Err()
		if err != nil {
			log.Printf("添加失败 [%s]: %v\n", link.ShortURL, err)
		}
	}

	log.Printf("Bloom 重建完成，共导入 %d 条记录\n", len(links))
}

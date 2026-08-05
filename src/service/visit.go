package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/CiroLong/shortlink/src/database"
	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
)

// VisitSyncConfig 访问统计同步配置
type VisitSyncConfig struct {
	BatchInterval        time.Duration // 批量同步间隔
	ThresholdInterval    time.Duration // 阈值检测频率
	VisitThreshold       int64         // 访问计数阈值
	BatchSize            int           // 批量处理大小
	BloomRebuildInterval time.Duration // 布隆过滤器重建间隔
}

// DefaultVisitSyncConfig 默认配置
var DefaultVisitSyncConfig = VisitSyncConfig{
	BatchInterval:        time.Hour,
	ThresholdInterval:    10 * time.Second,
	VisitThreshold:       5,
	BatchSize:            100,
	BloomRebuildInterval: 24 * time.Hour,
}

type VisitSyncer struct {
	config VisitSyncConfig
	db     *database.DB
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type pendingVisitCount struct {
	key   string
	count int64
}

// NewVisitSyncer 创建新的访问统计同步器
func NewVisitSyncer(config VisitSyncConfig) *VisitSyncer {
	ctx, cancel := context.WithCancel(context.Background())
	return &VisitSyncer{
		config: config,
		db:     database.GetDB(),
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start 启动所有后台任务
func (vs *VisitSyncer) Start() {
	vs.wg.Add(2)
	go vs.runVisitSync()
	go vs.runBloomFilterRebuild()
}

// Stop 优雅停止所有后台任务
func (vs *VisitSyncer) Stop() {
	vs.cancel()
	vs.wg.Wait()
	log.Println("所有后台任务已停止")
}

// runVisitSync 运行访问统计同步任务
func (vs *VisitSyncer) runVisitSync() {
	defer vs.wg.Done()

	batchTicker := time.NewTicker(vs.config.BatchInterval)
	thresholdTicker := time.NewTicker(vs.config.ThresholdInterval)
	defer batchTicker.Stop()
	defer thresholdTicker.Stop()

	for {
		select {
		case <-vs.ctx.Done():
			log.Println("访问统计同步任务正在停止...")
			return

		case <-batchTicker.C:
			if err := vs.performBatchSync(); err != nil {
				log.Printf("批量同步失败: %v\n", err)
			}
			if err := vs.cleanExpiredLinks(); err != nil {
				log.Printf("清理过期链接失败: %v\n", err)
			}

		case <-thresholdTicker.C:
			if err := vs.performThresholdSync(); err != nil {
				log.Printf("阈值同步失败: %v\n", err)
			}
		}
	}
}

// performBatchSync 执行批量同步
func (vs *VisitSyncer) performBatchSync() error {
	var cursor uint64
	for {
		keys, newCursor, err := vs.db.Redis.Scan(vs.ctx, cursor, "visit:*", int64(vs.config.BatchSize)).Result()
		if err != nil {
			return fmt.Errorf("扫描Redis键失败: %w", err)
		}

		if len(keys) > 0 {
			if err := vs.processBatch(keys); err != nil {
				return err
			}
		}

		cursor = newCursor
		if cursor == 0 {
			break
		}
	}
	return nil
}

// processBatch 处理一批访问记录
func (vs *VisitSyncer) processBatch(keys []string) error {
	pipe := vs.db.Redis.Pipeline()

	// 原子取出计数，后续访问会累加到新键，不会被本次同步删除。
	getCmds := make([]*redis.StringCmd, len(keys))
	for i, key := range keys {
		getCmds[i] = pipe.GetDel(vs.ctx, key)
	}

	_, execErr := pipe.Exec(vs.ctx)
	pending := make([]pendingVisitCount, 0, len(keys))
	var readErr error
	for i, key := range keys {
		count, err := getCmds[i].Int64()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			readErr = errors.Join(readErr, fmt.Errorf("获取访问计数失败 [%s]: %w", key, err))
			continue
		}
		if count > 0 {
			pending = append(pending, pendingVisitCount{key: key, count: count})
		}
	}
	if execErr != nil && !errors.Is(execErr, redis.Nil) && readErr == nil {
		readErr = fmt.Errorf("执行Redis管道失败: %w", execErr)
	}
	if readErr != nil {
		return errors.Join(readErr, vs.restoreVisitCounts(pending))
	}
	if len(pending) == 0 {
		return nil
	}

	// 批量更新MySQL；事务失败时将取出的计数合并回Redis。
	err := vs.db.MySql.Transaction(func(tx *gorm.DB) error {
		for _, visit := range pending {
			code := strings.TrimPrefix(visit.key, "visit:")
			if err := tx.Model(&Link{}).
				Where("short_url = ?", code).
				UpdateColumn("visit_count", gorm.Expr("visit_count + ?", visit.count)).
				Error; err != nil {
				return fmt.Errorf("更新MySQL访问计数失败 [%s]: %w", code, err)
			}
		}
		return nil
	})
	if err != nil {
		return errors.Join(err, vs.restoreVisitCounts(pending))
	}
	return nil
}

// performThresholdSync 执行阈值同步
func (vs *VisitSyncer) performThresholdSync() error {
	var cursor uint64
	for {
		keys, newCursor, err := vs.db.Redis.Scan(vs.ctx, cursor, "visit:*", int64(vs.config.BatchSize)).Result()
		if err != nil {
			return fmt.Errorf("扫描Redis键失败: %w", err)
		}

		for _, key := range keys {
			count, err := vs.db.Redis.Get(vs.ctx, key).Int64()
			if err != nil || count < vs.config.VisitThreshold {
				continue
			}

			code := strings.TrimPrefix(key, "visit:")
			if err := vs.syncSingleVisitCount(code); err != nil {
				log.Printf("同步单个访问计数失败 [%s]: %v\n", code, err)
			}
		}

		cursor = newCursor
		if cursor == 0 {
			break
		}
	}
	return nil
}

// syncSingleVisitCount 同步单个访问计数
func (vs *VisitSyncer) syncSingleVisitCount(code string) error {
	key := fmt.Sprintf("visit:%s", code)
	count, err := vs.takeVisitCount(key)
	if err != nil || count == 0 {
		return err
	}

	err = vs.db.MySql.Model(&Link{}).
		Where("short_url = ?", code).
		UpdateColumn("visit_count", gorm.Expr("visit_count + ?", count)).
		Error
	if err != nil {
		return errors.Join(err, vs.restoreVisitCounts([]pendingVisitCount{{key: key, count: count}}))
	}
	return nil
}

// takeVisitCount 原子取出计数，使之后的访问量保留在新的Redis键中。
func (vs *VisitSyncer) takeVisitCount(key string) (int64, error) {
	count, err := vs.db.Redis.GetDel(vs.ctx, key).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("获取访问计数失败 [%s]: %w", key, err)
	}
	return count, nil
}

// restoreVisitCounts 将同步失败的计数与期间新增的访问量合并。
func (vs *VisitSyncer) restoreVisitCounts(visits []pendingVisitCount) error {
	if len(visits) == 0 {
		return nil
	}

	pipe := vs.db.Redis.Pipeline()
	for _, visit := range visits {
		pipe.IncrBy(vs.ctx, visit.key, visit.count)
	}
	if _, err := pipe.Exec(vs.ctx); err != nil {
		return fmt.Errorf("恢复Redis访问计数失败: %w", err)
	}
	return nil
}

// cleanExpiredLinks 清理过期链接
func (vs *VisitSyncer) cleanExpiredLinks() error {
	now := time.Now().UTC()
	result := vs.db.MySql.
		Where("expire_at IS NOT NULL AND expire_at < ?", now).
		Delete(&Link{})

	if result.Error != nil {
		return fmt.Errorf("删除过期链接失败: %w", result.Error)
	}

	log.Printf("清理过期链接成功，共删除 %d 条\n", result.RowsAffected)
	return nil
}

// runBloomFilterRebuild 运行布隆过滤器重建任务
func (vs *VisitSyncer) runBloomFilterRebuild() {
	defer vs.wg.Done()

	ticker := time.NewTicker(vs.config.BloomRebuildInterval)
	defer ticker.Stop()

	// 启动时执行一次
	if err := vs.rebuildBloomFilter(); err != nil {
		log.Printf("重建布隆过滤器失败: %v\n", err)
	}

	for {
		select {
		case <-vs.ctx.Done():
			log.Println("布隆过滤器重建任务正在停止...")
			return
		case <-ticker.C:
			if err := vs.rebuildBloomFilter(); err != nil {
				log.Printf("重建布隆过滤器失败: %v\n", err)
			}
		}
	}
}

// rebuildBloomFilter 重建布隆过滤器
func (vs *VisitSyncer) rebuildBloomFilter() error {
	log.Println("开始重建布隆过滤器...")

	// 清空布隆过滤器
	if err := vs.db.Redis.Do(vs.ctx, "DEL", "bloom:shortlink").Err(); err != nil {
		return fmt.Errorf("删除旧布隆过滤器失败: %w", err)
	}

	// 分批查询未过期的链接
	var offset int
	for {
		var links []Link
		if err := vs.db.MySql.
			Model(&Link{}).
			Where("expire_at IS NULL OR expire_at > ?", time.Now()).
			Offset(offset).
			Limit(vs.config.BatchSize).
			Find(&links).Error; err != nil {
			return fmt.Errorf("查询链接失败: %w", err)
		}

		if len(links) == 0 {
			break
		}

		// 批量添加到布隆过滤器
		pipe := vs.db.Redis.Pipeline()
		for _, link := range links {
			pipe.Do(vs.ctx, "BF.ADD", "bloom:shortlink", link.ShortURL)
		}
		if _, err := pipe.Exec(vs.ctx); err != nil {
			log.Printf("添加到布隆过滤器失败: %v\n", err)
		}

		offset += len(links)
	}

	log.Printf("布隆过滤器重建完成，共导入 %d 条记录\n", offset)
	return nil
}

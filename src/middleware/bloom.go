package middleware

import (
	"github.com/CiroLong/shortlink/src/database"
	"net/http"

	"github.com/gin-gonic/gin"
)

// BloomFilterMiddleware 拦截不存在的短链接
func BloomFilterMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		code := c.Param("code")
		if code == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Empty short code"})
			return
		}

		// 查询 Bloom Filter
		exist, err := database.GetDB().Redis.Do(database.GetDB().Ctx, "BF.EXISTS", "bloom:shortlink", code).Bool()
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Bloom filter error"})
			return
		}

		if !exist {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "Short link not found"})
			return
		}

		// 存在，继续处理
		c.Next()
	}
}

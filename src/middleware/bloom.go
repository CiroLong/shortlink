package middleware

import (
	"net/http"

	"github.com/CiroLong/shortlink/src/database"

	"github.com/gin-gonic/gin"
)

// BloomFilterMiddleware 拦截不存在的短链接
func BloomFilterMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		code := c.Param("code")
		// code check 必须是8字符短连接
		if len(code) != 8 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "invalid short link format - must be 8 characters",
			})
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

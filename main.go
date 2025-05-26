package main

import (
	"fmt"
	"github.com/CiroLong/shortlink/src/config"
	"github.com/CiroLong/shortlink/src/database"
	"github.com/CiroLong/shortlink/src/handler"
	"github.com/CiroLong/shortlink/src/middleware"
	"github.com/CiroLong/shortlink/src/service"
	"github.com/gin-gonic/gin"
)

func main() {
	config.LoadConfig()

	database.InitDB()
	database.InitRedis()

	service.AutoMigrate()
	service.SyncVisitCounts()
	service.RebuildBloomFilter()

	r := gin.Default()
	r.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "This is an URL Shortener API",
		})
	})

	r.POST("/shorten", handler.ShortenURL)
	r.GET("/:code", handler.ResolveURL, middleware.BloomFilterMiddleware())

	if err := r.Run(":80"); err != nil {
		panic(fmt.Sprintf("Failed to start the web server - Error: %v", err))
	}
}

package main

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"shortlink/src/config"
	"shortlink/src/database"
	"shortlink/src/handler"
	"shortlink/src/service"
)

func main() {
	config.LoadConfig()

	database.InitDB()
	database.InitRedis()

	service.AutoMigrate()
	service.SyncVisitCounts()

	r := gin.Default()
	r.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "This is an URL Shortener API",
		})
	})

	r.POST("/shorten", handler.ShortenURL)
	r.GET("/:code", handler.ResolveURL)

	if err := r.Run(":8088"); err != nil {
		panic(fmt.Sprintf("Failed to start the web server - Error: %v", err))
	}
}

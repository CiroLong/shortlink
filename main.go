package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	syncer := service.NewVisitSyncer(service.DefaultVisitSyncConfig)
	syncer.Start()
	defer syncer.Stop()

	gin.SetMode(gin.ReleaseMode)

	r := gin.Default()

	{
		r.GET("/", func(c *gin.Context) {
			c.JSON(200, gin.H{
				"message": "This is an URL Shortener API",
			})
		})

		r.POST("/shorten", handler.ShortenURL)
		r.GET("/:code", handler.ResolveURL, middleware.BloomFilterMiddleware())
	}

	srv := &http.Server{
		Addr:           ":80",
		Handler:        r,
		ReadTimeout:    5 * time.Second,
		WriteTimeout:   10 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20,
		ErrorLog:       log.New(os.Stderr, "[HTTP Server] ", log.LstdFlags),
	}

	go func() {
		log.Printf("Server is starting on %s\n", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v\n", err)
		}
	}()

	// 优雅关停
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown: %v\n", err)
	} else {
		log.Println("Server stopped gracefully")
	}

	log.Println("Waiting for background tasks to complete...")
	log.Println("Server exiting")
}

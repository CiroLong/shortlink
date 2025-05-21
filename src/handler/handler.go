package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"shortlink/src/service"
)

func ShortenURL(c *gin.Context) {
	type UrlCreationRequest struct {
		LongUrl string `json:"long_url" binding:"required"`
		UserId  string `json:"user_id" binding:"required"`
	}
	var urlCreationRequest UrlCreationRequest
	if err := c.ShouldBind(&urlCreationRequest); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	shortURL := service.GenerateShortLink(urlCreationRequest.LongUrl, urlCreationRequest.UserId)

	err := service.SaveUrlMapping(shortURL, urlCreationRequest.LongUrl, urlCreationRequest.UserId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
	}

	host := "http://localhost/"
	c.JSON(http.StatusOK, gin.H{
		"message":   "short url created successfully",
		"short_url": host + shortURL,
	})
}

// ResolveURL GET "/:code"
func ResolveURL(c *gin.Context) {
	shortURL := c.Param("code")
	initialLink, err := service.RetrieveInitialUrl(shortURL)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "short link error",
		})
	}
	c.Redirect(http.StatusFound, initialLink)
}

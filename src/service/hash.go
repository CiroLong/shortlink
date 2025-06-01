package service

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"

	"github.com/CiroLong/shortlink/src/database"
	"github.com/itchyny/base58-go"
	"gorm.io/gorm"
)

const sha256Algorithm = "sha256"

var base58Encoding = base58.BitcoinEncoding

// computeSHA256Hash computes the SHA-256 hash of the input string.
func computeSHA256Hash(input string) []byte {
	hash := sha256.New()
	hash.Write([]byte(input))
	return hash.Sum(nil)
}

// encodeToBase58 encodes the given bytes into a Base58 string.
func encodeToBase58(bytes []byte) (string, error) {
	encoded, err := base58Encoding.Encode(bytes)
	if err != nil {
		return "", fmt.Errorf("failed to encode to Base58: %w", err)
	}
	return string(encoded), nil
}

// GenerateShortLink generates a short link from the given URL and ID.
func GenerateShortLink(url string, id string) (string, error) {
	var attempt int = 0
	for attempt < 3 {
		shortLink, err := generateWithSalt(url, id, attempt)
		if err != nil {
			return "", err
		}
		// 检查是否已存在
		exists, err := checkLinkExists(shortLink)
		if err != nil {
			return "", err
		}
		if !exists {
			return shortLink, nil
		}
		attempt++
	}
	return "", fmt.Errorf("failed to generate unique short link after %d attempts", attempt)

}

func generateWithSalt(url, id string, attempt int) (string, error) {
	// 添加salt来处理碰撞
	input := fmt.Sprintf("%s:%s:%d", url, id, attempt)
	urlHashBytes := computeSHA256Hash(input)

	generateNumber := new(big.Int).SetBytes(urlHashBytes).Uint64()
	encodedString, err := encodeToBase58([]byte(fmt.Sprintf("%d", generateNumber)))
	if err != nil {
		return "", fmt.Errorf("failed to generate short link: %w", err)
	}

	return encodedString[:8], nil
}

func checkLinkExists(shortLink string) (exists bool, err error) {
	// query MySQL
	db := database.GetDB()

	var link Link
	err = db.MySql.First(&link, shortLink).Error
	if err == nil {
		return true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return false, err
}

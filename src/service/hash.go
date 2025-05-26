package service

import (
	"crypto/sha256"
	"fmt"
	"github.com/itchyny/base58-go"
	"math/big"
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
	// Compute the SHA-256 hash of the concatenated URL and ID
	urlHashBytes := computeSHA256Hash(url + id)

	// Convert the hash bytes to a numeric value
	generateNumber := new(big.Int).SetBytes(urlHashBytes).Uint64()

	// Encode the numeric value to Base58
	encodedString, err := encodeToBase58([]byte(fmt.Sprintf("%d", generateNumber)))
	if err != nil {
		return "", fmt.Errorf("failed to generate short link: %w", err)
	}

	// Return the first 8 characters of the encoded string
	return encodedString[:8], nil
}

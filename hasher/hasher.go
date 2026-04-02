package hasher

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

const (
	HashSize = 10 * 1024 * 1024
)

func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return "", err
	}

	h := sha256.New()

	size := stat.Size()
	sizeBytes := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		sizeBytes[i] = byte(size & 0xff)
		size >>= 8
	}
	h.Write(sizeBytes)

	reader := io.LimitReader(f, HashSize)
	if _, err := io.Copy(h, reader); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

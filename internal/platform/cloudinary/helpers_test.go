package cloudinary_test

import (
	"crypto/sha1"
	"encoding/hex"
	"hash"
)

func newSHA1() hash.Hash    { return sha1.New() }
func hexOf(b []byte) string { return hex.EncodeToString(b) }

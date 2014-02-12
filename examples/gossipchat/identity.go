package main

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
)

// identity is the public identity of a peer.
type identity rsa.PublicKey

func (id identity) Fingerprint() fingerprint {
	pubKey := rsa.PublicKey(id)
	derEncoded, err := x509.MarshalPKIXPublicKey(&pubKey)
	if err != nil {
		panic(err)
	}
	return fingerprint(sha256.Sum256(derEncoded))
}

// fingerprint is a compact hash of an identity.
type fingerprint [sha256.Size]byte

func (fp fingerprint) String() string {
	return hex.EncodeToString(fp[:8])
}

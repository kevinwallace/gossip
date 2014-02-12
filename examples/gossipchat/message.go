package main

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/gob"
	"errors"
)

func init() {
	gob.Register(Message{})
	gob.Register(HelloPayload{})
	gob.Register(JoinPayload{})
	gob.Register(ChatPayload{})
}

// Message is a PKCS1 v1.5 signed message.
type Message struct {
	Origin       identity
	PayloadBytes []byte
	Signature    []byte
}

// NewMessage returns a new signed message containing the given payload.
func NewMessage(key *rsa.PrivateKey, payload interface{}) Message {
	var payloadBuf bytes.Buffer
	gob.NewEncoder(&payloadBuf).Encode(&payload)
	payloadBytes := payloadBuf.Bytes()
	hashed := sha256.Sum256(payloadBytes)
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hashed[:])
	if err != nil {
		panic(err)
	}
	return Message{
		Origin:       identity(key.PublicKey),
		PayloadBytes: payloadBytes,
		Signature:    sig,
	}
}

// Verify verifies that the message's signature matches its origin,
// returning an error when it doesn't.
func (m Message) Verify() error {
	if len(m.Signature) == 0 {
		return errors.New("empty signature")
	}
	hashed := sha256.Sum256(m.PayloadBytes)
	pubKey := rsa.PublicKey(m.Origin)
	return rsa.VerifyPKCS1v15(&pubKey, crypto.SHA256, hashed[:], m.Signature)
}

// Payload decodes the payload bytes to a Go struct.
func (m Message) Payload() (interface{}, error) {
	var value interface{}
	err := gob.NewDecoder(bytes.NewBuffer(m.PayloadBytes)).Decode(&value)
	return value, err
}

// HelloPayload is sent by a peer when it establishes a connection with another.
type HelloPayload struct {
	YourAddr string
}

// JoinPayload is sent by a peer when it joins the chat.
type JoinPayload struct {
	Addr string
}

// ChatPayload is sent by a peer when it has a message to broadcast.
type ChatPayload struct {
	Sequence sequencer
	Text     string
}

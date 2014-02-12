package main

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"sync"

	"github.com/kevinwallace/gossip"
)

// identityRecord contains everything we know about an identity.
type identityRecord struct {
	Identity            identity
	ChatSequenceTracker sequenceTracker
}

// Chat is the local peer's view of a chat session.
type Chat struct {
	key      *rsa.PrivateKey
	port     int
	gossiper gossip.Gossiper

	// mu guards the following fields.
	mu           sync.Mutex
	joinsByAddr  map[string]Message
	identities   map[fingerprint]*identityRecord
	chatSequence sequencer
}

// NewChat returns a new chat using the given private key as its identity,
// and advertising itself on the given TCP port.
func NewChat(key *rsa.PrivateKey, port int) *Chat {
	return &Chat{
		key:         key,
		port:        port,
		joinsByAddr: make(map[string]Message),
		identities:  make(map[fingerprint]*identityRecord),
	}
}

// SendChat broadcasts a chat message with the given text.
func (c *Chat) SendChat(text string) {
	c.mu.Lock()
	seq := c.chatSequence
	c.chatSequence++
	c.mu.Unlock()
	c.gossiper.Broadcast(NewMessage(c.key, ChatPayload{
		Sequence: seq,
		Text:     text,
	}))
}

// handleGossip handles an incoming gossip message.
// The message is decapsulated, verified, then dispatched to the corresponding handle* method.
func (c *Chat) handleGossip(value interface{}) bool {
	msg, ok := value.(Message)
	if !ok {
		log.Printf("Discarding unexpected gossip value: %#v", value)
		return false
	}

	if err := msg.Verify(); err != nil {
		log.Printf("Discarding message with invalid signature. err: %s; msg: %v", err, msg)
		return false
	}

	payload, err := msg.Payload()
	if err != nil {
		log.Printf("Discarding message with invalid payload: %s", err)
	}

	id := msg.Origin
	fp := id.Fingerprint()
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.identities[fp]; !ok {
		c.identities[fp] = &identityRecord{
			Identity: id,
		}
	}
	record := c.identities[fp]

	switch payload := payload.(type) {
	case HelloPayload:
		return c.handleHello(record, payload, msg)
	case JoinPayload:
		return c.handleJoin(record, payload, msg)
	case ChatPayload:
		return c.handleChat(record, payload, msg)
	default:
		log.Printf("Discarding message with unknown payload: %#v", payload)
		return false
	}
}

func (c *Chat) handleHello(record *identityRecord, payload HelloPayload, msg Message) bool {
	tcpAddr, err := net.ResolveTCPAddr("tcp", payload.YourAddr)
	if err != nil {
		log.Printf("Received hello with invalid TCP address: %s", payload.YourAddr)
		return false
	}
	tcpAddr.Port = c.port
	go c.gossiper.Broadcast(NewMessage(c.key, JoinPayload{
		Addr: tcpAddr.String(),
	}))
	return false
}

func (c *Chat) handleJoin(record *identityRecord, payload JoinPayload, msg Message) bool {
	_, ok := c.joinsByAddr[payload.Addr]
	if !ok {
		c.joinsByAddr[payload.Addr] = msg
		fmt.Printf("* %s joined from %s\n", record.Identity.Fingerprint(), payload.Addr)
	}
	return !ok
}

func (c *Chat) handleChat(record *identityRecord, payload ChatPayload, msg Message) bool {
	isNew := record.ChatSequenceTracker.See(payload.Sequence)
	if isNew {
		fmt.Printf("<%s> %s\n", record.Identity.Fingerprint(), payload.Text)
	}
	return isNew
}

func (c *Chat) findPeer(connectedPeers map[gossip.PeerHandle]gossip.Peer) (gossip.Peer, error) {
	var addrs []string
	c.mu.Lock()
	for addr := range c.joinsByAddr {
		addrs = append(addrs, addr)
	}
	c.mu.Unlock()
	if len(addrs) == 0 {
		return nil, gossip.ErrNoPeers
	}
	addr := addrs[rand.Intn(len(addrs))]
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		c.mu.Lock()
		delete(c.joinsByAddr, addr)
		c.mu.Unlock()
		return nil, err
	}
	return NewPeer(conn), nil
}

func (c *Chat) onConnect(peer gossip.Peer) error {
	chatPeer, ok := peer.(*ChatPeer)
	if !ok {
		return errors.New("not a ChatPeer")
	}
	var joins []Message
	c.mu.Lock()
	for _, join := range c.joinsByAddr {
		joins = append(joins, join)
	}
	c.mu.Unlock()
	hello := NewMessage(c.key, HelloPayload{
		YourAddr: chatPeer.conn.RemoteAddr().String(),
	})
	if err := peer.Write(hello); err != nil {
		return err
	}
	for _, join := range joins {
		if err := peer.Write(join); err != nil {
			return err
		}
	}
	return nil
}

package main

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"flag"
	"fmt"
	"log"
	"net"
	"os"

	"github.com/kevinwallace/gossip"
)

var port = flag.Int("port", 5353, "port to listen on")

// listen listens on the given address,
// establishing gossip sessions with incoming connections,
// then handing them off to peerManager
func listen(addr string, peerManager gossip.PeerManager) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		panic(err)
	}
	defer listener.Close()
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Error accepting connection: %s", err)
		}
		peer := NewPeer(conn)
		if err := peerManager.AddPeer(peer); err != nil {
			log.Printf("Error adding new peer %s: %s", peer, err)
		}
	}
}

// connect establishes gossip sessions with the given addresses,
// handing them off to peerManager once established.
func connect(addrs []string, peerManager gossip.PeerManager) {
	for _, addr := range addrs {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			log.Fatalf("Error dialing initial peer %s: %s", addr, err)
		}
		peer := NewPeer(conn)
		if err := peerManager.AddPeer(peer); err != nil {
			log.Fatalf("Error adding initial peer %s: %s", peer, err)
		}
	}
}

// readLines loops, reading each line from stdin and broadcasting it as a chat message.
// It returns on EOF.
func readlines(c *Chat) {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		c.SendChat(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		panic(err)
	}
}

func main() {
	flag.Parse()

	fmt.Println("Generating identity...")
	key, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Identity generated: %s\n", identity(key.PublicKey).Fingerprint())

	c := NewChat(key, *port)
	c.gossiper = gossip.NewGossiper(c.handleGossip)
	defer c.gossiper.Close()

	peerManager := gossip.NewPeerManager(c.gossiper, gossip.PeerManagerConfig{
		NewPeer:   c.findPeer,
		OnConnect: c.onConnect,
		MinPeers:  1,
	})

	go listen(fmt.Sprintf(":%d", *port), peerManager)
	connect(flag.Args(), peerManager)
	readlines(c)
}

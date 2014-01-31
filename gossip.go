// Package gossip implements a gossip-based multicast protocol.
package gossip

import (
	"errors"
	"io"
	"log"
	"sync"
)

// ErrClosed is returned from operations that cannot be completed because the Gossiper is closed.
var ErrClosed = errors.New("shutting down")

// Gossiper is a single local node in the gossip protocol.
type Gossiper interface {
	// Broadcast broadcasts the given message to all connected peers.
	// The gossiper's updateFunc will be called with this message before it is broadcast,
	// but it will be broadcast even if updateFunc returns false.
	Broadcast(message interface{})
	// AddPeer adds the given peer to the set of connected peers.
	AddPeer(peer Peer) (PeerHandle, error)
	// RemovePeer attempts to close the given peer, and removes it from the set of connected peers.
	RemovePeer(handle PeerHandle)
	// AddPeerWatcher adds the given PeerWatcher to the list of PeerWatchers to
	// notify upon changes to the set of connected peers.
	AddPeerWatcher(PeerWatcher)
	// Close shuts down the gossiper, and closes any connected peers.
	Close()
}

// Peer is the interface to a remote peer.
type Peer interface {
	// Read a message from this peer.
	// Return io.EOF if the connection was cleanly closed.
	Read() (interface{}, error)
	// Write a message to this peer.
	// Return io.EOF if the connection was cleanly closed.
	Write(interface{}) error
	// Close the connection to this peer.  Subsequent calls are no-ops.
	// After this is called, all blocked calls to Read and Write will unblock.
	Close() error
}

// PeerWatcher is an interface implemented by clients who want to know when to the peer set changes.
// Callbacks are called while holding a gossiper-internal lock, and must not call back into Gossiper.
type PeerWatcher interface {
	// PeerAdded is called when a peer is added.
	PeerAdded(handle PeerHandle, peer Peer)
	// PeerRemoved is called when a peer is removed.
	PeerRemoved(handle PeerHandle, peer Peer)
}

// PeerHandle is an opaque handle that references a peer we are gossiping with.
type PeerHandle uint

// selfHandle refers to the local node.
const selfHandle PeerHandle = 0

// peerHandleStart is the first usable handle for peers.
const peerHandleStart PeerHandle = 1

type incomingMessage struct {
	message    interface{}
	peerHandle PeerHandle
}

type outgoingMessage struct {
	message      interface{}
	excludePeers map[PeerHandle]bool
}

type gossiper struct {
	incomingMessages chan incomingMessage
	outgoingMessages chan outgoingMessage
	peerWatchers     []PeerWatcher

	mu             sync.Mutex
	nextPeerHandle PeerHandle
	peers          map[PeerHandle]Peer

	closing         bool
	peerClosedChans map[PeerHandle]chan<- bool
	closed          chan bool
}

// NewGossiper sets up a local gossiper.
// Each incoming message is passed to updateFunc.
// If it returns true, the message is propagated to our other neighbors.  Otherwise, it is dropped.
// Calls to updateFunc are synchronized.
func NewGossiper(updateFunc func(interface{}) bool) Gossiper {
	g := &gossiper{
		incomingMessages: make(chan incomingMessage, 1000),
		outgoingMessages: make(chan outgoingMessage, 1000),
		peerWatchers:     nil,
		nextPeerHandle:   peerHandleStart,
		peers:            make(map[PeerHandle]Peer),
		peerClosedChans:  make(map[PeerHandle]chan<- bool),
		closed:           make(chan bool),
	}
	go g.pumpIncoming(updateFunc)
	go g.pumpOutgoing()
	return g
}

func (g *gossiper) AddPeer(peer Peer) (handle PeerHandle, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.closing {
		err = ErrClosed
		return
	}

	handle = g.nextPeerHandle
	g.nextPeerHandle++
	g.peers[handle] = peer

	for _, peerWatcher := range g.peerWatchers {
		peerWatcher.PeerAdded(handle, peer)
	}

	go g.pumpPeerIncoming(handle, peer)
	return
}

func (g *gossiper) pumpOutgoing() {
	for outgoingMessage := range g.outgoingMessages {
		g.mu.Lock()
		peers := make(map[PeerHandle]Peer)
		for handle, peer := range g.peers {
			peers[handle] = peer
		}
		g.mu.Unlock()

		for handle, peer := range peers {
			if outgoingMessage.excludePeers[handle] {
				continue
			}
			err := peer.Write(outgoingMessage.message)
			if err != nil {
				if err != io.EOF {
					log.Printf("error writing message to peer %s: %s", peer, err)
				}
				g.RemovePeer(handle)
			}
		}
	}
	close(g.closed)
}

func (g *gossiper) pumpIncoming(updateFunc func(interface{}) bool) {
	for incomingMessage := range g.incomingMessages {
		if updateFunc(incomingMessage.message) || incomingMessage.peerHandle == selfHandle {
			g.outgoingMessages <- outgoingMessage{
				message:      incomingMessage.message,
				excludePeers: map[PeerHandle]bool{incomingMessage.peerHandle: true},
			}
		}
	}
	close(g.outgoingMessages)
}

// pumpPeerSingleIncoming sends a single message on incomingMessages,
// unless the gossiper is currently shutting down.
// It returns true if the message was sent.
func (g *gossiper) pumpPeerSingleIncoming(msg incomingMessage) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closing {
		return false
	}
	g.incomingMessages <- msg
	return true
}

func (g *gossiper) pumpPeerIncoming(handle PeerHandle, peer Peer) {
	for {
		message, err := peer.Read()
		if err != nil {
			if err != io.EOF {
				log.Printf("error reading from peer %s; disconnecting: %s", peer, err)
			}
			break
		}
		msg := incomingMessage{message, handle}
		if !g.pumpPeerSingleIncoming(msg) {
			break
		}
	}
	g.RemovePeer(handle)
}

func (g *gossiper) RemovePeer(handle PeerHandle) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if peer, ok := g.peers[handle]; ok {
		if err := peer.Close(); err != nil {
			log.Printf("error closing peer %s: %s", peer, err)
		}
		delete(g.peers, handle)
		for _, peerWatcher := range g.peerWatchers {
			peerWatcher.PeerRemoved(handle, peer)
		}
		if c, ok := g.peerClosedChans[handle]; ok {
			c <- true
			delete(g.peerClosedChans, handle)
		}
	}
}

func (g *gossiper) Broadcast(message interface{}) {
	g.incomingMessages <- incomingMessage{
		message:    message,
		peerHandle: selfHandle,
	}
}

func (g *gossiper) AddPeerWatcher(peerWatcher PeerWatcher) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.peerWatchers = append(g.peerWatchers, peerWatcher)
}

func (g *gossiper) Close() {
	g.mu.Lock()
	g.closing = true
	nPeers := len(g.peers)
	c := make(chan bool)
	for handle, peer := range g.peers {
		g.peerClosedChans[handle] = c
		if err := peer.Close(); err != nil {
			log.Printf("error closing peer %s: %s", peer, err)
		}
	}
	g.mu.Unlock()

	for i := 0; i < nPeers; i++ {
		<-c
	}

	// Close the incoming messages channel and let it drain.
	close(g.incomingMessages)

	// Once it drains, pumpIncoming closes the outgoing messages channel,
	// which drains and causes pumpOutgoing to signal on g.closed.
	<-g.closed
}

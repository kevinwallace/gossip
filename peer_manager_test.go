package gossip

import (
	"fmt"
	"io"
	"sync"
	"testing"
	"time"
)

// mockPeer is a peer that discards all writes, and blocks on reads until it is closed.
type mockPeer struct {
	name   string
	mu     sync.Mutex
	closed chan bool
}

func (mp *mockPeer) String() string {
	return mp.name
}

func (mp *mockPeer) Read() (interface{}, error) {
	<-mp.closed
	return nil, io.EOF
}

func (mp *mockPeer) Write(interface{}) error {
	select {
	case <-mp.closed:
		return io.EOF
	default:
		return nil
	}
}

func (mp *mockPeer) Close() error {
	mp.mu.Lock()
	defer mp.mu.Unlock()
	select {
	case <-mp.closed:
	default:
		close(mp.closed)
	}
	return nil
}

func newMockPeerFunc(namePrefix string) func(map[PeerHandle]Peer) (Peer, error) {
	i := 0
	return func(map[PeerHandle]Peer) (Peer, error) {
		i++
		return &mockPeer{
			name:   fmt.Sprintf("%s%d", namePrefix, i),
			closed: make(chan bool),
		}, nil
	}
}

func setupPeerManager(config PeerManagerConfig) (Gossiper, PeerManager) {
	gossiper := NewGossiper(func(interface{}) bool { return false })
	peerManager := NewPeerManager(gossiper, config)
	return gossiper, peerManager
}

func waitForCondition(t *testing.T, timeout time.Duration, condText string, cond func() bool) {
	if cond() {
		return
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if cond() {
			return
		}
	}
	t.Fatalf("gave up after waiting %s for %s", timeout, condText)
}

// TestConnectUnderconnected tests that a PeerManager connects more peers when
// the number of peers is less than MinPeers.
func TestConnectUnderconnected(t *testing.T) {
	g, pm := setupPeerManager(PeerManagerConfig{
		NewPeer:  newMockPeerFunc("managerAdded"),
		MinPeers: 3,
	})
	defer g.Close()
	defer pm.Close()
	waitForCondition(t, 10*time.Second, "manager to connect 3 peers", func() bool {
		return len(pm.(*peerManager).currentPeersCopy()) >= 3
	})
}

// TestConnectUnderconnected tests that a PeerManager disconnects peers when
// the number of peers exceeds MaxPeers.
func TestDisconnectOverconnected(t *testing.T) {
	peerFunc := newMockPeerFunc("manuallyAdded")
	g, pm := setupPeerManager(PeerManagerConfig{
		NewPeer:  newMockPeerFunc("managerAdded"),
		MaxPeers: 5,
	})
	defer g.Close()
	defer pm.Close()
	for i := 0; i < 10; i++ {
		peer, err := peerFunc(nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := g.AddPeer(peer); err != nil {
			t.Fatal(err)
		}
	}
	waitForCondition(t, 10*time.Second, "manager to disconnect excess peers", func() bool {
		return len(pm.(*peerManager).currentPeersCopy()) <= 5
	})
}

package gossip

import (
	"errors"
	"log"
	"math/rand"
	"sync"
	"time"
)

// DefaultRetryDelay is the default duration to wait before retrying failed operations.
// See PeerManagerConfig's RetryDelay.
const DefaultRetryDelay = 5 * time.Second

// ErrNoPeers can be returned by the NewPeer callback in order to indicate that
// the application doesn't know about any additional peers.
var ErrNoPeers = errors.New("no peers known")

// PeerManager is responsible for managing the set of currently-connected peers.
type PeerManager interface {
	// AddPeer adds the given peer to the underlying gossiper, transferring ownership,
	// and performs a full state dump to that peer.
	AddPeer(peer Peer) error
	// Close shuts down this PeerManager.
	// Once it returns, this PeerManager will not make any further calls to Gossiper.
	Close()
}

// PeerManagerConfig contains parameters for constructing a new PeerManager.
type PeerManagerConfig struct {
	// NewPeer is called in order to connect to a new, random peer.
	// The list of currently-connected peers is given so that they can be
	// avoided when choosing a new peer.
	// If nil, no new peers will be added automatically.
	NewPeer func(currentPeers map[PeerHandle]Peer) (Peer, error)
	// OnConnect is called when a new peer is added,
	// in order to e.g. perform a directed state dump to a new peer.
	// If nil, nothing extra is done when a new peer is added.
	OnConnect func(peer Peer) error
	// MinPeers and MaxPeers specify a bound on the number of connected peers.
	// If zero, it is unbounded at that end.
	MinPeers, MaxPeers int
	// RetryDelay is the duration to wait before retrying failed operations,
	// such as when NewPeer or OnConnect return an error.
	// If zero, defaults to DefaultRetryDelay.
	RetryDelay time.Duration
}

// NewPeerManager creates a new PeerManager managing the given underlying gossiper.
func NewPeerManager(gossiper Gossiper, config PeerManagerConfig) PeerManager {
	pm := &peerManager{
		gossiper:     gossiper,
		newPeer:      config.NewPeer,
		onConnect:    config.OnConnect,
		minPeers:     config.MinPeers,
		maxPeers:     config.MaxPeers,
		retryDelay:   config.RetryDelay,
		currentPeers: make(map[PeerHandle]Peer),
		notifyChan:   make(chan bool, 1),
		closed:       make(chan bool, 1),
	}

	if pm.retryDelay == 0 {
		pm.retryDelay = DefaultRetryDelay
	}

	pm.gossiperWatchHandle = gossiper.AddPeerWatcher(pm)
	go pm.watchPeers()
	pm.notify()
	return pm
}

type peerManager struct {
	gossiper            Gossiper
	gossiperWatchHandle PeerWatcherHandle

	newPeer            func(currentPeers map[PeerHandle]Peer) (Peer, error)
	onConnect          func(peer Peer) error
	minPeers, maxPeers int
	retryDelay         time.Duration

	// mu guards the remaining fields.
	mu           sync.Mutex
	currentPeers map[PeerHandle]Peer
	// notifyChan carries notifications to the watchPeers goroutine that
	// currentPeers may have changed.
	notifyChan chan bool

	// closing is true if Close has been called.
	closing bool
	// watchPeers will send over this channel once it's shut down.
	closed chan bool
}

func (pm *peerManager) AddPeer(peer Peer) error {
	if _, err := pm.gossiper.AddPeer(peer); err != nil {
		peer.Close()
		return err
	}
	if pm.onConnect != nil {
		if err := pm.onConnect(peer); err != nil {
			peer.Close()
			return err
		}
	}
	return nil
}

func (pm *peerManager) Close() {
	pm.mu.Lock()
	if !pm.closing {
		close(pm.notifyChan)
		pm.gossiper.RemovePeerWatcher(pm.gossiperWatchHandle)
		pm.closing = true
	}
	pm.mu.Unlock()
	<-pm.closed
}

func (pm *peerManager) notify() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pm.closing {
		// We're in the process of shutting down.
		// We don't care about this notification.
		return
	}
	select {
	case pm.notifyChan <- true:
	default:
		// There's already a notification pending --
		// when it's picked up, watchPeers will see our changes too.
	}
}

func (pm *peerManager) notifyAfter(d time.Duration) {
	go func() {
		time.Sleep(d)
		pm.notify()
	}()
}

// watchPeers watches the set of current peers, adding or removing peers when it becomes too small or large.
// It acts whenever a value is sent over pm.notifyChan, and shuts down when the channel is closed.
func (pm *peerManager) watchPeers() {
	for _ = range pm.notifyChan {
		currentPeers := pm.currentPeersCopy()
		n := len(currentPeers)
		if n < pm.minPeers {
			pm.findPeerToAdd(currentPeers)
		} else if pm.maxPeers != 0 && n > pm.maxPeers {
			pm.findPeerToRemove(currentPeers)
		}
	}
	pm.closed <- true
}

func (pm *peerManager) findPeerToAdd(currentPeers map[PeerHandle]Peer) {
	if pm.newPeer == nil {
		return
	}
	peer, err := pm.newPeer(currentPeers)
	if err != nil {
		if err != ErrNoPeers {
			log.Printf("Error finding a new peer to add. Retrying in %s. err: %s", pm.retryDelay, err)
		}
		pm.notifyAfter(pm.retryDelay)
		return
	}
	if err := pm.AddPeer(peer); err != nil {
		log.Printf("Error adding new peer %s. Retrying in %s. err: %s", peer, pm.retryDelay, err)
		pm.notifyAfter(pm.retryDelay)
		return
	}
}

func (pm *peerManager) findPeerToRemove(currentPeers map[PeerHandle]Peer) {
	var victimHandle PeerHandle
	var victim Peer
	var ok bool
	var i int
	for handle, peer := range currentPeers {
		i++
		if rand.Intn(i) == 0 {
			victimHandle = handle
			victim = peer
			ok = true
		}
	}
	if ok {
		log.Printf("Too many connected peers; removing %s.", victim)
		pm.gossiper.RemovePeer(victimHandle)
	}
}

func (pm *peerManager) currentPeersCopy() map[PeerHandle]Peer {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	currentPeers := make(map[PeerHandle]Peer, len(pm.currentPeers))
	for handle, peer := range pm.currentPeers {
		currentPeers[handle] = peer
	}
	return currentPeers
}

// PeerAdded is called by Gossiper when a peer is added.
func (pm *peerManager) PeerAdded(handle PeerHandle, peer Peer) {
	pm.mu.Lock()
	pm.currentPeers[handle] = peer
	pm.mu.Unlock()
	pm.notify()
}

// PeerAdded is called by Gossiper when a peer is removed.
func (pm *peerManager) PeerRemoved(handle PeerHandle, peer Peer) {
	pm.mu.Lock()
	delete(pm.currentPeers, handle)
	pm.mu.Unlock()
	pm.notify()
}

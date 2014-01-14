package gossip

import (
	"fmt"
	"io"
	"log"
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"time"
)

type pipePeer struct {
	name     string
	closedMu sync.Mutex
	closed   chan bool
	incoming chan interface{}
	outgoing chan interface{}
}

func (p pipePeer) String() string { return p.name }

func (p pipePeer) Read() (interface{}, error) {
	select {
	case <-p.closed:
		return nil, io.EOF
	case message := <-p.incoming:
		return message, nil
	}
}

func (p pipePeer) Write(message interface{}) error {
	select {
	case <-p.closed:
		return io.EOF
	case p.outgoing <- message:
	}
	return nil
}

func (p pipePeer) Close() error {
	p.closedMu.Lock()
	defer p.closedMu.Unlock()
	select {
	case <-p.closed:
	default:
		close(p.closed)
	}
	return nil
}

func makePeerPipe(aName, bName string) (Peer, Peer) {
	namePrefix := "pipe-" + aName + "-" + bName + "-"
	a := &pipePeer{
		name:     namePrefix + bName,
		closed:   make(chan bool),
		incoming: make(chan interface{}),
		outgoing: make(chan interface{}),
	}
	b := &pipePeer{
		name:     namePrefix + aName,
		closed:   make(chan bool),
		incoming: a.outgoing,
		outgoing: a.incoming,
	}
	return a, b
}

type testNode struct {
	name              string
	gossiper          Gossiper
	peerNames         map[string]bool
	mu                sync.Mutex
	seen              map[interface{}]bool
	notificationChans map[interface{}]chan<- bool
}

func (n *testNode) update(message interface{}) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.seen[message] {
		n.seen[message] = true
		if c := n.notificationChans[message]; c != nil {
			c <- true
		}
		return true
	}
	return false
}

func (n *testNode) Await(message interface{}) {
	c := make(chan bool, 1)
	n.mu.Lock()
	seen := n.seen[message]
	if !seen {
		n.notificationChans[message] = c
	} else {
		c <- true
	}
	n.mu.Unlock()
	<-c
}

func makeNode(name string) *testNode {
	node := &testNode{
		name:              name,
		peerNames:         make(map[string]bool),
		seen:              make(map[interface{}]bool),
		notificationChans: make(map[interface{}]chan<- bool),
	}
	node.gossiper = NewGossiper(node.update, Config{})
	return node
}

func connectNodes(a, b *testNode) {
	aPeer, bPeer := makePeerPipe(a.name, b.name)
	a.gossiper.AddPeer(aPeer)
	b.gossiper.AddPeer(bPeer)
	a.peerNames[b.name] = true
	b.peerNames[a.name] = true
}

func chooseNode(nodes []*testNode, f func(testNode) bool) (chosenNode *testNode, ok bool) {
	i := 1
	for _, node := range nodes {
		if f(*node) {
			if rand.Intn(i) == 0 {
				chosenNode = node
				ok = true
			}
			i++
		}
	}
	return
}

func makeNetwork(r *rand.Rand, numNodes, minPeers, maxPeers int) []*testNode {
	nodes := make([]*testNode, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = makeNode(fmt.Sprintf("node%d", i))
	}

	// Connect a random minimum spanning tree of nodes.
	for i := 1; i < numNodes; i++ {
		node := nodes[i]
		otherNode, ok := chooseNode(nodes[:i], func(n testNode) bool { return len(n.peerNames) < maxPeers })
		if !ok {
			log.Fatalf("no eligible nodes to connect to to form a spanning tree")
		}
		connectNodes(node, otherNode)
	}

	// Now that we're fully-connected, connect each under-connected node randomly.
	for _, node := range nodes {
		for len(node.peerNames) < minPeers {
			otherNode, ok := chooseNode(nodes, func(n testNode) bool {
				return n.name != node.name && !n.peerNames[node.name] && len(n.peerNames) < maxPeers
			})
			if !ok {
				log.Fatalf("no eligible nodes to connect to to ensure minimum connectivity %#v", node)
			}
			connectNodes(node, otherNode)
		}
	}

	return nodes
}

func testPropagation(t *testing.T, nodes []*testNode) {
	for _, node := range nodes {
		node.gossiper.Broadcast(node.name)
	}
	for _, node := range nodes {
		t.Logf("Awaiting %s", node.name)
		for _, node2 := range nodes {
			node.Await(node2.name)
		}
	}
}

func benchmarkPropagation(b *testing.B, nodes []*testNode) {
	nodes[0].gossiper.Broadcast("foo")
	for _, node := range nodes {
		b.Logf("Awaiting %s", node.name)
		node.Await("foo")
	}
}

func tearDown(nodes []*testNode) {
	for _, node := range nodes {
		node.gossiper.Close()
	}
}

func checkForGoroutineLeaks(startingGoroutines int) {
	// TODO this is gross; do something more precise along the lines of
	// https://codereview.appspot.com/7777043/diff/22001/src/pkg/net/http/z_last_test.go?context=10&column_width=80
	start := time.Now()
	deadline := start.Add(5 * time.Second)
	var goroutines int
	for time.Now().Before(deadline) {
		goroutines = runtime.NumGoroutine()
		// go test seems to spawn one goroutine during our test, so let a single extra goroutine slide.
		if goroutines <= startingGoroutines+1 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	log.Fatalf("Have %d goroutines after tearing down, was %d before setup", goroutines, startingGoroutines)
}

func TestPropagationSmallFullyConnectedNetwork(t *testing.T) {
	defer checkForGoroutineLeaks(runtime.NumGoroutine())
	r := rand.New(rand.NewSource(1))
	nodes := makeNetwork(r, 10, 9, 9)
	testPropagation(t, nodes)
	tearDown(nodes)
}

func TestPropagationLargeSparselyConnectedNetwork(t *testing.T) {
	defer checkForGoroutineLeaks(runtime.NumGoroutine())
	r := rand.New(rand.NewSource(1))
	nodes := makeNetwork(r, 500, 1, 2)
	testPropagation(t, nodes)
	tearDown(nodes)
}

func TestPropagationMediumWellConnectedNetwork(t *testing.T) {
	defer checkForGoroutineLeaks(runtime.NumGoroutine())
	r := rand.New(rand.NewSource(1))
	nodes := makeNetwork(r, 200, 8, 10)
	testPropagation(t, nodes)
	tearDown(nodes)
}

func BenchmarkSmallFullyConnectedNetwork(b *testing.B) {
	defer checkForGoroutineLeaks(runtime.NumGoroutine())
	r := rand.New(rand.NewSource(1))
	nodes := makeNetwork(r, 10, 9, 10)
	benchmarkPropagation(b, nodes)
	tearDown(nodes)
}

func BenchmarkLargeSparselyConnectedNetwork(b *testing.B) {
	defer checkForGoroutineLeaks(runtime.NumGoroutine())
	r := rand.New(rand.NewSource(1))
	nodes := makeNetwork(r, 500, 1, 2)
	benchmarkPropagation(b, nodes)
	tearDown(nodes)
}

func BenchmarkLargeWellConnectedNetwork(b *testing.B) {
	defer checkForGoroutineLeaks(runtime.NumGoroutine())
	r := rand.New(rand.NewSource(1))
	nodes := makeNetwork(r, 500, 8, 10)
	benchmarkPropagation(b, nodes)
	tearDown(nodes)
}

func BenchmarkHugeSparselyConnectedNetwork(b *testing.B) {
	defer checkForGoroutineLeaks(runtime.NumGoroutine())
	r := rand.New(rand.NewSource(1))
	nodes := makeNetwork(r, 10000, 1, 2)
	benchmarkPropagation(b, nodes)
	tearDown(nodes)
}

type testPeerWatcher map[PeerHandle]Peer

func (pw testPeerWatcher) PeerAdded(handle PeerHandle, peer Peer) {
	pw[handle] = peer
}

func (pw testPeerWatcher) PeerRemoved(handle PeerHandle, peer Peer) {
	delete(pw, handle)
}

func (pw testPeerWatcher) Contains(handle PeerHandle) bool {
	_, ok := pw[handle]
	return ok
}

func TestPeerWatcherNotified(t *testing.T) {
	defer checkForGoroutineLeaks(runtime.NumGoroutine())
	pw := testPeerWatcher{}
	config := Config{
		PeerWatcher: pw,
	}
	peer, _ := makePeerPipe("a", "b")
	gossiper := NewGossiper(func(_ interface{}) bool { return true }, config)

	if len(pw) != 0 {
		t.Errorf("pw non-empty before adding any peers: %v", pw)
	}

	handle, err := gossiper.AddPeer(peer)
	if err != nil {
		t.Fatalf("unable to add peer: %s", err)
	}
	if !pw.Contains(handle) {
		t.Errorf("pw doesn't contain peer after adding: %v", pw)
	}
	gossiper.RemovePeer(handle)
	if pw.Contains(handle) {
		t.Errorf("pw contains peer after removing: %v", pw)
	}
	gossiper.Close()
}

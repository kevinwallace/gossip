gossip
======
[![Build Status](https://travis-ci.org/kevinwallace/gossip.png?branch=master)](https://travis-ci.org/kevinwallace/gossip)
[![GoDoc](https://godoc.org/github.com/kevinwallace/gossip?status.png)](https://godoc.org/github.com/kevinwallace/gossip)

Gossip is a Go library that implements a gossip-based multicast protocol using a
mesh of long-lived connections between nodes.

Usage
-----

Define a function to be called every time a new value is gossiped, then create a
new gossiper using that function.

    gossipFunc := func(msg interface{}) bool {
      log.Printf("Gossiped value: %#v", msg)
      return false
    }
    gossiper := gossip.NewGossiper(gossipFunc)

Then, connect the gossiper up to other peers using `gossiper.AddPeer`, or by
handing your gossiper to a `PeerManager`, which tries to maintain connections
with a minimum number of other peers.

You'll need to define your own implementation of `Peer` to match your
application's transport.

See `examples/gossipchat` for a more complete example.
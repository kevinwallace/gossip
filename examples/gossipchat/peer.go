package main

import (
	"encoding/gob"
	"net"
)

// ChatPeer implements gossip.Peer, sending each message gob-encoded over the wire.
type ChatPeer struct {
	conn net.Conn
	enc  *gob.Encoder
	dec  *gob.Decoder
}

// NewPeer returns a new ChatPeer, backed by the given net.Conn.
func NewPeer(conn net.Conn) *ChatPeer {
	return &ChatPeer{
		conn: conn,
		enc:  gob.NewEncoder(conn),
		dec:  gob.NewDecoder(conn),
	}
}

// String returns a string representation of this peer.
func (p *ChatPeer) String() string {
	return p.conn.RemoteAddr().String()
}

// Write sends a message to the remote peer.
func (p *ChatPeer) Write(msg interface{}) error {
	return p.enc.Encode(&msg)
}

// Read receives a message from the remote peer.
func (p *ChatPeer) Read() (interface{}, error) {
	var val interface{}
	err := p.dec.Decode(&val)
	return val, err
}

// Close closes the connection to the remote peer.
func (p *ChatPeer) Close() error {
	return p.conn.Close()
}

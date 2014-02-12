gossipchat
==========

`gossipchat` is a simple chat program that uses `gossip` as its transport. Each
node generates an RSA identity and uses it to sign its own messages, to prevent
impersonation.  The protocol provides no confidentiality -- messages are sent
over the network in the clear.

Usage
-----

    $ go install github.com/kevinwallace/gossip/examples/gossipchat
    $ gossipchat --port=1234

In another terminal (or on another host):

    $ gossipchat --port=1235 localhost:1234

Repeat for as many nodes as you want, then have fun talking to yourself.

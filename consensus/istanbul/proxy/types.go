// Copyright 2017 The celo Authors
// This file is part of the celo library.
//
// The celo library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The celo library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the celo library. If not, see <http://www.gnu.org/licenses/>.

package proxy

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/istanbul"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/rlp"
)

var (
	// errUnauthorizedMessageFromProxiedValidator is returned when the received message expected to be signed
	// by the proxied validator, but signed from another key
	errUnauthorizedMessageFromProxiedValidator = errors.New("message not signed by proxied validator")

	// errInvalidEnodeCertificate is returned if the enode certificate is invalid
	errInvalidEnodeCertificate = errors.New("invalid enode certificate")

	// errUnauthorizedProxiedValidatorPeer is returned if the peer connecting is not an authorized
	// proxy for this validator.
	errUnauthorizedProxiedValidatorPeer = errors.New("connection from unauthorized peer")

	// ErrStoppedProxyHandler is returned if proxy handler is stopped
	ErrStoppedProxyHandler = errors.New("proxy handler is not running")

	// ErrStartedProxyHandler is returned if proxy handler is already started
	ErrStartedProxyHandler = errors.New("proxy handler is running")

	// ErrNodeNotProxiedValidator is returned if this node is not a proxied validator
	ErrNodeNotProxiedValidator = errors.New("node not a proxied validator")

	// ErrNodeNotProxy is returned if this node is not a proxy
	ErrNodeNotProxy = errors.New("node not a proxy")
)

type ProxyEngine interface {
	// HandleMsg is the `celo` subprotocol message handler for proxies.
	HandleMsg(peer consensus.Peer, msgCode uint64, payload []byte) (bool, error)

	// RegisterProxiedValidatorPeer is the callback function that should be called
	// when a proxied validator connects to a proxy.  This function will save
	// the proxied validator's peer in the proxy's state.
	RegisterProxiedValidatorPeer(proxiedValidatorPeer consensus.Peer)

	// UnregisterProxiedValidatorPeer is the callback function that should be
	// called when a proxied validator disconnects from a proxy.  This function
	// will remove the proxied validator's peer from the proxy's state.
	UnregisterProxiedValidatorPeer(proxiedValidatorPeer consensus.Peer)

	// SendDelegateSignMsgToProxiedValidator(msg []byte) error

	// SendEnodeCertificateMsgToProxiedValidator will send the given enode certificate
	// message to the proxied validator.
	SendEnodeCertificateMsgToProxiedValidator(msg *istanbul.Message) error

	// GetProxiedValidatorsInfo will return information about the proxied validator.
	GetProxiedValidatorsInfo() ([]*ProxiedValidatorInfo, error)
}

type ProxiedValidatorEngine interface {
	// Start will start the proxied validator engine. Specifically, it will start the
	// proxy handler thread.
	Start() error

	// Stop will stop the proxied validator engine. Specifically, it will stop the
	// proxy handler thread.
	Stop() error

	// AddProxy will add a new proxy to the proxy handler
	AddProxy(node, externalNode *enode.Node) error

	// RemoveProxy will remove a proxy from the proxy handler
	RemoveProxy(node *enode.Node) error

	// RegisterProxyPeer is the callback function that should be called
	// when a proxy connects to a proxied validator.  This function will
	// notify the proxy handler that a proxy has connected.
	RegisterProxyPeer(proxyPeer consensus.Peer) error

	// UnregisterProxyPeer is the callback function that should be called
	// when a proxy is disconnected from a proxied validator.  This function will
	// notify the proxy handler that a proxy has disconnected.
	UnregisterProxyPeer(proxyPeer consensus.Peer)

	// sendValEnodesShareMsg will send a val enode share messages with the val enode table entries associated
	// with the remoteValidators to the proxyPeer.
	sendValEnodesShareMsg(proxyPeer consensus.Peer, remoteValidators []common.Address) error

	// SendForwardMsg will send a forward message.
	SendForwardMsg(proxyPeers []consensus.Peer, finalDestAddresses []common.Address, ethMsgCode uint64, payload []byte, proxySpecificPayload map[enode.ID][]byte) error

	// SendDelegateSignMsgToProxy(msg []byte) error

	// SendValEnodeShareMsgToAllProxies will send the appropriate val enode share message to each
	// connected proxy.
	SendValEnodesShareMsgToAllProxies()

	// GetValidatorProxyAssignments will retrieve all of the remote validator to proxy assignments.
	GetValidatorProxyAssignments() (map[common.Address]*enode.Node, error)

	// GetProxiesAndValAssignments will retrieve all of the proxies (connected or not yet connected) and
	// the proxy to validator assignments.
	GetProxiesAndValAssignments() ([]*proxy, map[enode.ID][]common.Address, error)
}

// ==============================================
//
// define the proxy object

type proxy struct {
	node         *enode.Node    // Enode for the internal network interface
	externalNode *enode.Node    // Enode for the external network interface
	peer         consensus.Peer // Connected proxy peer.  Is nil if this node is not connected to the proxy
	disconnectTS time.Time      // Timestamp when this proxy's peer last disconnected. Initially set to the timestamp of when the proxy was added
}

func (p *proxy) ID() enode.ID {
	return p.node.ID()
}

func (p *proxy) ExternalNode() *enode.Node {
	return p.externalNode
}

func (p *proxy) IsPeered() bool {
	return p.peer != nil
}

func (p *proxy) String() string {
	return fmt.Sprintf("{internalNode: %v, externalNode %v, dcTimestamp: %v, ID: %v}", p.node, p.externalNode, p.disconnectTS, p.ID())
}

// ProxyInfo is used to provide info on a proxy that can be given via an RPC
type ProxyInfo struct {
	InternalNode             *enode.Node      `json:"internalEnodeUrl"`
	ExternalNode             *enode.Node      `json:"externalEnodeUrl"`
	IsPeered                 bool             `json:"isPeered"`
	AssignedRemoteValidators []common.Address `json:"validators"`            // All validator addresses assigned to the proxy
	DisconnectTS             int64            `json:"disconnectedTimestamp"` // Unix time of the last disconnect of the peer
}

func NewProxyInfo(p *proxy, assignedVals []common.Address) *ProxyInfo {
	return &ProxyInfo{
		InternalNode:             p.node,
		ExternalNode:             p.ExternalNode(),
		IsPeered:                 p.IsPeered(),
		DisconnectTS:             p.disconnectTS.Unix(),
		AssignedRemoteValidators: assignedVals,
	}
}

// ==============================================
//
// define the proxied validator info object

type ProxiedValidatorInfo struct {
	Address  common.Address `json:"address"`
	IsPeered bool           `json:"isPeered"`
	Node     *enode.Node    `json:"enodeURL"`
}

// ==============================================
//
// define the validator enode share message

type sharedValidatorEnode struct {
	Address  common.Address
	EnodeURL string
	Version  uint
}

type valEnodesShareData struct {
	ValEnodes []sharedValidatorEnode
}

func (sve *sharedValidatorEnode) String() string {
	return fmt.Sprintf("{Address: %s, EnodeURL: %v, Version: %v}", sve.Address.Hex(), sve.EnodeURL, sve.Version)
}

func (sd *valEnodesShareData) String() string {
	outputStr := "{ValEnodes:"
	for _, valEnode := range sd.ValEnodes {
		outputStr = fmt.Sprintf("%s %s", outputStr, valEnode.String())
	}
	return fmt.Sprintf("%s}", outputStr)
}

// ==============================================
//
// define the functions that needs to be provided for rlp Encoder/Decoder.

// EncodeRLP serializes sd into the Ethereum RLP format.
func (sd *valEnodesShareData) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, []interface{}{sd.ValEnodes})
}

// DecodeRLP implements rlp.Decoder, and load the sd fields from a RLP stream.
func (sd *valEnodesShareData) DecodeRLP(s *rlp.Stream) error {
	var msg struct {
		ValEnodes []sharedValidatorEnode
	}

	if err := s.Decode(&msg); err != nil {
		return err
	}
	sd.ValEnodes = msg.ValEnodes
	return nil
}

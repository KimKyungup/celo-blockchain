// Copyright 2017 The Celo Authors
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
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/istanbul"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/rlp"
)

func (pv *proxiedValidatorEngine) SendForwardMsg(proxyPeers []consensus.Peer, validatorAddresses []common.Address, ethMsgCode uint64, payload []byte, proxySpecificPayloads map[enode.ID][]byte) error {
	logger := pv.logger.New("func", "SendForwardMsg")

	logger.Info("Sending forward msg", "ethMsgCode", ethMsgCode, "validatorAddresses", common.ConvertToStringSlice(validatorAddresses))

	proxyToAddressesMap := make(map[consensus.Peer][]common.Address)

	// If the proxy peers are not given to this function, then retrieve them via the proxy handler
	if proxyPeers == nil {
		valAssignments, err := pv.ph.GetValidatorAssignments(validatorAddresses)
		if err != nil {
			logger.Warn("Got an error when trying to retrieve validator assignments", "err", err)
			return err
		}

		// Create proxy -> set of validator addresses map
		for valAddress, proxy := range valAssignments {
			if proxy != nil && proxy.peer != nil {
				if proxyToAddressesMap[proxy.peer] == nil {
					proxyToAddressesMap[proxy.peer] = make([]common.Address, 0)
				}

				proxyToAddressesMap[proxy.peer] = append(proxyToAddressesMap[proxy.peer], valAddress)
			}
		}

		if len(proxyToAddressesMap) == 0 {
			logger.Warn("No proxy assigned to any of the final dest addresses for sending a fwd message", "ethMsgCode", ethMsgCode, "finalDestAddreses", common.ConvertToStringSlice(validatorAddresses))
			return nil
		}
	} else {
		for _, proxyPeer := range proxyPeers {
			proxyToAddressesMap[proxyPeer] = nil
		}
	}

	// Send the forward messages to the proxies
	for proxyPeer, valAddresses := range proxyToAddressesMap {
		// Convert the message to a fwdMessage

		msgToForward := payload

		if proxySpecificPayload, ok := proxySpecificPayloads[proxyPeer.Node().ID()]; ok {
			msgToForward = proxySpecificPayload
		}

		if msgToForward != nil {
			fwdMessage := &istanbul.ForwardMessage{
				Code:          ethMsgCode,
				DestAddresses: valAddresses,
				Msg:           msgToForward,
			}
			fwdMsgBytes, err := rlp.EncodeToBytes(fwdMessage)
			if err != nil {
				logger.Error("Failed to encode", "fwdMessage", fwdMessage)
				return err
			}

			// Note that we are not signing message.  The message that is being wrapped is already signed.
			msg := istanbul.Message{Code: istanbul.FwdMsg, Msg: fwdMsgBytes, Address: pv.backend.Address()}
			fwdMsgPayload, err := msg.Payload()
			if err != nil {
				return err
			}

			pv.backend.Unicast(proxyPeer, fwdMsgPayload, istanbul.FwdMsg)
		}
	}

	return nil
}

func (p *proxyEngine) handleForwardMsg(peer consensus.Peer, payload []byte) (bool, error) {
	logger := p.logger.New("func", "HandleForwardMsg")

	logger.Trace("Handling a forward message")

	// Verify that it's coming from the proxied validator
	if p.proxiedValidator == nil || p.proxiedValidator.Node().ID() != peer.Node().ID() {
		logger.Warn("Got a forward consensus message from a peer that is not the proxy's proxied validator. Ignoring it", "from", peer.Node().ID())
		return false, nil
	}

	istMsg := new(istanbul.Message)

	// An Istanbul FwdMsg doesn't have a signature since it's coming from a trusted peer and
	// the wrapped message is already signed by the proxied validator.
	if err := istMsg.FromPayload(payload, nil); err != nil {
		logger.Error("Failed to decode message from payload", "from", peer.Node().ID(), "err", err)
		return true, err
	}

	var fwdMsg *istanbul.ForwardMessage
	err := istMsg.Decode(&fwdMsg)
	if err != nil {
		logger.Error("Failed to decode a ForwardMessage", "from", peer.Node().ID(), "err", err)
		return true, err
	}

	logger.Trace("Forward message's code", "fwdMsg.Code", fwdMsg.Code)

	// If this is a EnodeCertificateMsg, then do additional handling.
	// TODO: Ideally there should be a separate protocol between proxy and proxied validator
	//       and as part of that protocol, it has a specific message code for a proxied validator
	//       to share to all of it's proxies their enode certificate.
	if fwdMsg.Code == istanbul.EnodeCertificateMsg {
		logger.Trace("Doing special handling for a forwarded enode certficate msg")
		if err := p.handleEnodeCertificateFromFwdMsg(fwdMsg.Msg); err != nil {
			logger.Error("Error in handling enode certificate msg from forward msg", "from", peer.Node().ID(), "err", err)
			return true, err
		}
	}

	logger.Trace("Forwarding a message", "msg code", fwdMsg.Code)
	if err := p.backend.Multicast(fwdMsg.DestAddresses, fwdMsg.Msg, fwdMsg.Code, false); err != nil {
		logger.Error("Error in multicasting a forwarded message", "error", err)
		return true, err
	}

	return true, nil
}

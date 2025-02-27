// Copyright 2017 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package core

import (
	"math/big"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/istanbul"
	"github.com/ethereum/go-ethereum/consensus/istanbul/validator"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/bls"
)

func TestVerifyPreparedCertificate(t *testing.T) {
	N := uint64(4) // replica 0 is the proposer, it will send messages to others
	F := uint64(1)
	sys := NewTestSystemWithBackend(N, F)
	view := istanbul.View{
		Round:    big.NewInt(0),
		Sequence: big.NewInt(1),
	}
	proposal := makeBlock(0)

	testCases := []struct {
		certificate istanbul.PreparedCertificate
		expectedErr error
	}{
		{
			// Valid PREPARED certificate
			sys.getPreparedCertificate(t, view, proposal),
			nil,
		},
		{
			// Invalid PREPARED certificate, duplicate message
			func() istanbul.PreparedCertificate {
				preparedCertificate := sys.getPreparedCertificate(t, view, proposal)
				preparedCertificate.PrepareOrCommitMessages[1] = preparedCertificate.PrepareOrCommitMessages[0]
				return preparedCertificate
			}(),
			errInvalidPreparedCertificateDuplicate,
		},
		{
			// Invalid PREPARED certificate, future message
			func() istanbul.PreparedCertificate {
				futureView := istanbul.View{
					Round:    big.NewInt(0),
					Sequence: big.NewInt(10),
				}
				preparedCertificate := sys.getPreparedCertificate(t, futureView, proposal)
				return preparedCertificate
			}(),
			errInvalidPreparedCertificateMsgView,
		},
		{
			// Invalid PREPARED certificate, includes preprepare message
			func() istanbul.PreparedCertificate {
				preparedCertificate := sys.getPreparedCertificate(t, view, proposal)
				testInvalidMsg, _ := sys.backends[0].getRoundChangeMessage(view, sys.getPreparedCertificate(t, view, proposal))
				preparedCertificate.PrepareOrCommitMessages[0] = testInvalidMsg
				return preparedCertificate
			}(),
			errInvalidPreparedCertificateMsgCode,
		},
		{
			// Invalid PREPARED certificate, hash mismatch
			func() istanbul.PreparedCertificate {
				preparedCertificate := sys.getPreparedCertificate(t, view, proposal)
				preparedCertificate.PrepareOrCommitMessages[1] = preparedCertificate.PrepareOrCommitMessages[0]
				preparedCertificate.Proposal = makeBlock(1)
				return preparedCertificate
			}(),
			errInvalidPreparedCertificateDigestMismatch,
		},
		{
			// Empty certificate
			istanbul.EmptyPreparedCertificate(),
			errInvalidPreparedCertificateNumMsgs,
		},
	}
	for _, test := range testCases {
		for _, backend := range sys.backends {
			c := backend.engine.(*core)
			err := c.verifyPreparedCertificate(test.certificate)
			if err != test.expectedErr {
				t.Errorf("error mismatch: have %v, want %v", err, test.expectedErr)
			}
		}
	}
}

func TestHandlePrepare(t *testing.T) {
	N := uint64(4)
	F := uint64(1)

	proposal := newTestProposal()
	expectedSubject := &istanbul.Subject{
		View: &istanbul.View{
			Round:    big.NewInt(0),
			Sequence: proposal.Number(),
		},
		Digest: proposal.Hash(),
	}

	testCases := []struct {
		system      *testSystem
		expectedErr error
	}{
		{
			// normal case
			func() *testSystem {
				sys := NewTestSystemWithBackend(N, F)

				for i, backend := range sys.backends {
					c := backend.engine.(*core)
					c.valSet = backend.peers
					c.current = newTestRoundState(
						&istanbul.View{
							Round:    big.NewInt(0),
							Sequence: big.NewInt(1),
						},
						c.valSet,
					)

					if i == 0 {
						// replica 0 is the proposer
						c.state = StatePreprepared
					}
				}
				return sys
			}(),
			nil,
		},
		{
			// normal case with prepared certificate
			func() *testSystem {
				sys := NewTestSystemWithBackend(N, F)
				preparedCert := sys.getPreparedCertificate(
					t,
					istanbul.View{
						Round:    big.NewInt(0),
						Sequence: big.NewInt(1),
					},
					proposal)

				for i, backend := range sys.backends {
					c := backend.engine.(*core)
					c.valSet = backend.peers
					c.current = newTestRoundState(
						&istanbul.View{
							Round:    big.NewInt(0),
							Sequence: big.NewInt(1),
						},
						c.valSet,
					)
					c.current.preparedCertificate = preparedCert

					if i == 0 {
						// replica 0 is the proposer
						c.state = StatePreprepared
					}
				}
				return sys
			}(),
			nil,
		},
		{
			// Inconsistent subject due to prepared certificate
			func() *testSystem {
				sys := NewTestSystemWithBackend(N, F)
				preparedCert := sys.getPreparedCertificate(
					t,
					istanbul.View{
						Round:    big.NewInt(0),
						Sequence: big.NewInt(10),
					},
					proposal)

				for i, backend := range sys.backends {
					c := backend.engine.(*core)
					c.valSet = backend.peers
					c.current = newTestRoundState(
						&istanbul.View{
							Round:    big.NewInt(0),
							Sequence: big.NewInt(1),
						},
						c.valSet,
					)
					c.current.preparedCertificate = preparedCert

					if i == 0 {
						// replica 0 is the proposer
						c.state = StatePreprepared
					}
				}
				return sys
			}(),
			errInconsistentSubject,
		},
		{
			// future message
			func() *testSystem {
				sys := NewTestSystemWithBackend(N, F)

				for i, backend := range sys.backends {
					c := backend.engine.(*core)
					c.valSet = backend.peers
					if i == 0 {
						// replica 0 is the proposer
						c.current = newTestRoundState(
							expectedSubject.View,
							c.valSet,
						)
						c.state = StatePreprepared
					} else {
						c.current = newTestRoundState(
							&istanbul.View{
								Round:    big.NewInt(2),
								Sequence: big.NewInt(3),
							},
							c.valSet,
						)
					}
				}
				return sys
			}(),
			errFutureMessage,
		},
		{
			// subject not match
			func() *testSystem {
				sys := NewTestSystemWithBackend(N, F)

				for i, backend := range sys.backends {
					c := backend.engine.(*core)
					c.valSet = backend.peers
					if i == 0 {
						// replica 0 is the proposer
						c.current = newTestRoundState(
							expectedSubject.View,
							c.valSet,
						)
						c.state = StatePreprepared
					} else {
						c.current = newTestRoundState(
							&istanbul.View{
								Round:    big.NewInt(0),
								Sequence: big.NewInt(0),
							},
							c.valSet,
						)
					}
				}
				return sys
			}(),
			errOldMessage,
		},
		{
			// subject not match
			func() *testSystem {
				sys := NewTestSystemWithBackend(N, F)

				for i, backend := range sys.backends {
					c := backend.engine.(*core)
					c.valSet = backend.peers
					if i == 0 {
						// replica 0 is the proposer
						c.current = newTestRoundState(
							expectedSubject.View,
							c.valSet,
						)
						c.state = StatePreprepared
					} else {
						c.current = newTestRoundState(
							&istanbul.View{
								Round:    big.NewInt(0),
								Sequence: big.NewInt(1)},
							c.valSet,
						)
					}
				}
				return sys
			}(),
			errInconsistentSubject,
		},
		{
			// less than 2F+1
			func() *testSystem {
				sys := NewTestSystemWithBackend(N, F)

				// save less than 2*F+1 replica
				sys.backends = sys.backends[2*int(F)+1:]

				for i, backend := range sys.backends {
					c := backend.engine.(*core)
					c.valSet = backend.peers
					c.current = newTestRoundState(
						expectedSubject.View,
						c.valSet,
					)

					if i == 0 {
						// replica 0 is the proposer
						c.state = StatePreprepared
					}
				}
				return sys
			}(),
			nil,
		},
		// TODO: double send message
	}

OUTER:
	for _, test := range testCases {
		test.system.Run(false)

		v0 := test.system.backends[0]
		r0 := v0.engine.(*core)

		for i, v := range test.system.backends {
			validator := r0.valSet.GetByIndex(uint64(i))
			m, _ := Encode(v.engine.(*core).current.Subject())
			if err := r0.handlePrepare(&istanbul.Message{
				Code:    istanbul.MsgPrepare,
				Msg:     m,
				Address: validator.Address(),
			}); err != nil {
				if err != test.expectedErr {
					t.Errorf("error mismatch: have %v, want %v", err, test.expectedErr)
				}
				continue OUTER
			}
		}

		// prepared is normal case
		if r0.state != StatePrepared {
			// There are not enough PREPARE messages in core
			if r0.state != StatePreprepared {
				t.Errorf("state mismatch: have %v, want %v", r0.state, StatePreprepared)
			}
			if r0.current.Prepares.Size() >= r0.valSet.MinQuorumSize() {
				t.Errorf("the size of PREPARE messages should be less than %v", 2*r0.valSet.MinQuorumSize()+1)
			}

			continue
		}

		// core should have MinQuorumSize PREPARE messages
		if r0.current.Prepares.Size() < r0.valSet.MinQuorumSize() {
			t.Errorf("the size of PREPARE messages should be greater than or equal to MinQuorumSize: size %v", r0.current.Prepares.Size())
		}

		// a message will be delivered to backend if 2F+1
		if int64(len(v0.sentMsgs)) != 1 {
			t.Errorf("the Send() should be called once: times %v", len(test.system.backends[0].sentMsgs))
		}

		// verify COMMIT messages
		decodedMsg := new(istanbul.Message)
		err := decodedMsg.FromPayload(v0.sentMsgs[0], nil)
		if err != nil {
			t.Errorf("error mismatch: have %v, want nil", err)
		}

		if decodedMsg.Code != istanbul.MsgCommit {
			t.Errorf("message code mismatch: have %v, want %v", decodedMsg.Code, istanbul.MsgCommit)
		}
		var m *istanbul.Subject
		err = decodedMsg.Decode(&m)
		if err != nil {
			t.Errorf("error mismatch: have %v, want nil", err)
		}
		if !reflect.DeepEqual(m, expectedSubject) {
			t.Errorf("subject mismatch: have %v, want %v", m, expectedSubject)
		}
	}
}

// round is not checked for now
func TestVerifyPrepare(t *testing.T) {
	// for log purpose
	privateKey, _ := crypto.GenerateKey()
	blsPrivateKey, _ := blscrypto.ECDSAToBLS(privateKey)
	blsPublicKey, _ := blscrypto.PrivateToPublic(blsPrivateKey)
	peer := validator.New(getPublicKeyAddress(privateKey), blsPublicKey)
	valSet := validator.NewSet([]istanbul.ValidatorData{
		{
			peer.Address(),
			blsPublicKey,
		},
	}, istanbul.RoundRobin)

	sys := NewTestSystemWithBackend(uint64(1), uint64(0))

	testCases := []struct {
		expected error

		prepare    *istanbul.Subject
		roundState *roundState
	}{
		{
			// normal case
			expected: nil,
			prepare: &istanbul.Subject{
				View:   &istanbul.View{Round: big.NewInt(0), Sequence: big.NewInt(0)},
				Digest: newTestProposal().Hash(),
			},
			roundState: newTestRoundState(
				&istanbul.View{Round: big.NewInt(0), Sequence: big.NewInt(0)},
				valSet,
			),
		},
		{
			// old message
			expected: errInconsistentSubject,
			prepare: &istanbul.Subject{
				View:   &istanbul.View{Round: big.NewInt(0), Sequence: big.NewInt(0)},
				Digest: newTestProposal().Hash(),
			},
			roundState: newTestRoundState(
				&istanbul.View{Round: big.NewInt(1), Sequence: big.NewInt(1)},
				valSet,
			),
		},
		{
			// different digest
			expected: errInconsistentSubject,
			prepare: &istanbul.Subject{
				View:   &istanbul.View{Round: big.NewInt(0), Sequence: big.NewInt(0)},
				Digest: common.BytesToHash([]byte("1234567890")),
			},
			roundState: newTestRoundState(
				&istanbul.View{Round: big.NewInt(1), Sequence: big.NewInt(1)},
				valSet,
			),
		},
		{
			// malicious package(lack of sequence)
			expected: errInconsistentSubject,
			prepare: &istanbul.Subject{
				View:   &istanbul.View{Round: big.NewInt(0), Sequence: nil},
				Digest: newTestProposal().Hash(),
			},
			roundState: newTestRoundState(
				&istanbul.View{Round: big.NewInt(1), Sequence: big.NewInt(1)},
				valSet,
			),
		},
		{
			// wrong PREPARE message with same sequence but different round
			expected: errInconsistentSubject,
			prepare: &istanbul.Subject{
				View:   &istanbul.View{Round: big.NewInt(1), Sequence: big.NewInt(0)},
				Digest: newTestProposal().Hash(),
			},
			roundState: newTestRoundState(
				&istanbul.View{Round: big.NewInt(0), Sequence: big.NewInt(0)},
				valSet,
			),
		},
		{
			// wrong PREPARE message with same round but different sequence
			expected: errInconsistentSubject,
			prepare: &istanbul.Subject{
				View:   &istanbul.View{Round: big.NewInt(0), Sequence: big.NewInt(1)},
				Digest: newTestProposal().Hash(),
			},
			roundState: newTestRoundState(
				&istanbul.View{Round: big.NewInt(0), Sequence: big.NewInt(0)},
				valSet,
			),
		},
	}
	for i, test := range testCases {
		c := sys.backends[0].engine.(*core)
		c.current = test.roundState

		if err := c.verifyPrepare(test.prepare); err != nil {
			if err != test.expected {
				t.Errorf("result %d: error mismatch: have %v, want %v", i, err, test.expected)
			}
		}
	}
}

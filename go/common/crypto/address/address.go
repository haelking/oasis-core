// Package address implements address
package address

import (
	"bytes"
	"encoding/base64"
	"errors"

	"github.com/oasislabs/oasis-core/go/common/crypto/signature"
)

const (
	// Addresses are 20 bytes long.
	Size = 20
)

var (
	// ErrMalformed is the error returned when an address is malformed.
	ErrMalformed = errors.New("hash: malformed address")
)

type Address [Size]byte

// MarshalBinary encodes an address into binary form.
func (a *Address) MarshalBinary() (data []byte, err error) {
	data = append([]byte{}, a[:]...)
	return
}

// UnmarshalBinary decodes a binary marshaled address.
func (a *Address) UnmarshalBinary(data []byte) error {
	if len(data) != Size {
		return ErrMalformed
	}

	copy(a[:], data)

	return nil
}

// MarshalText encodes an address into text form.
func (a Address) MarshalText() (data []byte, err error) {
	// TODO: Replace this with Bech32.
	return []byte(base64.StdEncoding.EncodeToString(a[:])), nil
}

// UnmarshalText decodes a text marshaled address.
func (a *Address) UnmarshalText(text []byte) error {
	// TODO: Replace this with Bech32.
	b, err := base64.StdEncoding.DecodeString(string(text))
	if err != nil {
		return err
	}

	return a.UnmarshalBinary(b)
}

// Equal compares vs another address for equality.
func (a *Address) Equal(cmp *Address) bool {
	return bytes.Equal(a[:], cmp[:])
}

// String returns a string representation of the public key.
func (a Address) String() string {
	// TODO: Replace this with Bech32.
	b64Addr := base64.StdEncoding.EncodeToString(a[:])

	if len(a) != Size {
		return "[malformed]: " + b64Addr
	}

	return b64Addr
}

// // MarshalText encodes a Hash into text form.
// func (h Hash) MarshalText() (data []byte, err error) {
// 	return []byte(base64.StdEncoding.EncodeToString(h[:])), nil
// }

// // UnmarshalText decodes a text marshaled Hash.
// func (h *Hash) UnmarshalText(text []byte) error {
// 	b, err := base64.StdEncoding.DecodeString(string(text))
// 	if err != nil {
// 		return err
// 	}

// 	return h.UnmarshalBinary(b)
// }

// func (a Address) FromBytes(data ...[]byte) {
// 	h := hash.NewFromBytes(data[:])

// 	h2 = h[:AddressSize]
// 	return
// }

func NewFromPublicKey(pk signature.PublicKey) (a Address) {
	truncatedHash, err := pk.Hash().Truncate(Size)
	if err != nil {
		panic(err)
	}
	_ = a.UnmarshalBinary(truncatedHash)
	return
}

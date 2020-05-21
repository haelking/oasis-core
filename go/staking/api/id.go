package api

import (
	"github.com/oasislabs/oasis-core/go/common/crypto/address"
	"github.com/oasislabs/oasis-core/go/common/crypto/signature"
)

// ID is the staking account's id.
type ID address.Address

// MarshalText encodes an id into text form.
func (id *ID) MarshalText() ([]byte, error) {
	return (*address.Address)(id).MarshalText()
}

// UnmarshalText decodes a text marshaled id.
func (id *ID) UnmarshalText(text []byte) error {
	return (*address.Address)(id).UnmarshalText(text)
}

// Equal compares vs another id for equality.
func (id *ID) Equal(cmp *ID) bool {
	return (*address.Address)(id).Equal((*address.Address)(cmp))
}

// String returns the string representation of an id.
func (id ID) String() string {
	return address.Address(id).String()
}

func NewIDFromPublicKey(pk signature.PublicKey) (id ID) {
	return (ID)(address.NewFromPublicKey(pk))
}

// func (id AccountID) Equal(cmp AccountID) bool {
// 	return id.Equal(cmp)
// }
